//go:build linux

package main

import (
	"archive/tar"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// must is a helper function that panics on error
func must(e error) {
	if e != nil {
		log.Fatal(e)
	}
}

// rootfs downloads an OCI image and returns the unpacked directory path
// Always downloads fresh (no caching)
func rootfs(ref string) string {
	// Parse the image reference (e.g., "alpine:latest")
	r, err := name.ParseReference(ref)
	must(err)

	// Create a unique temporary directory for this run
	tmpDir := fmt.Sprintf("/tmp/rootfs-%d", time.Now().UnixNano())
	must(os.MkdirAll(tmpDir, 0755))

	fmt.Printf("Downloading and unpacking %s to %s\n", ref, tmpDir)

	// Download the image and unpack it
	img, err := remote.Image(r)
	must(err)
	must(unpack(img, tmpDir))
	return tmpDir
}

// unpack extracts all layers of an OCI image to the destination directory
func unpack(img v1.Image, dst string) error {
	layers, err := img.Layers()
	if err != nil {
		return err
	}

	// Extract each layer in order
	for _, l := range layers {
		rc, err := l.Uncompressed()
		if err != nil {
			return err
		}
		if err := untar(rc, dst); err != nil {
			rc.Close()
			return err
		}
		rc.Close()
	}
	return nil
}

// untar extracts a tar stream to the destination directory
// This is a pure Go implementation that handles the most common tar entry types
func untar(r io.Reader, dst string) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		path := filepath.Join(dst, h.Name)

		switch h.Typeflag {
		case tar.TypeDir:
			must(os.MkdirAll(path, os.FileMode(h.Mode)))
		case tar.TypeReg:
			must(os.MkdirAll(filepath.Dir(path), 0755))
			f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, os.FileMode(h.Mode))
			must(err)
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		case tar.TypeLink:
			// Hard link within the image
			must(os.Link(filepath.Join(dst, h.Linkname), path))
		case tar.TypeSymlink:
			must(os.Symlink(h.Linkname, path))
		}
	}
}

// child runs the containerized process with the new namespaces
// This function executes inside the isolated container environment
func child(rootfs string) {
	// Set a friendly hostname for the container
	must(unix.Sethostname([]byte("shadman-lab")))

	// Mount /proc filesystem inside the rootfs before chroot
	// This is necessary because /proc won't be available after chroot
	procPath := filepath.Join(rootfs, "proc")
	must(os.MkdirAll(procPath, 0755))
	must(unix.Mount("proc", procPath, "proc",
		uintptr(unix.MS_NOSUID|unix.MS_NOEXEC|unix.MS_NODEV), ""))

	// Use chroot instead of pivot_root for simplicity
	// chroot is easier to use and sufficient for basic containerization
	// It changes the root directory for the current process and its children
	must(unix.Chroot(rootfs))

	// Change to the new root directory
	must(unix.Chdir("/"))

	// Execute the shell as PID 1 inside the container
	// The -i flag makes the shell interactive
	must(syscall.Exec("/bin/sh", []string{"sh", "-i"}, os.Environ()))
}

func main() {
	// Check if this is the child process before parsing flags
	// The child process is created with "--child" as the first argument
	if len(os.Args) > 1 && os.Args[1] == "--child" {
		child(os.Args[2])
		return
	}

	// Parse command line flags for the parent process
	img := flag.String("image", "alpine:latest", "OCI image reference to run")
	userns := flag.Bool("userns", false, "enable user namespace (for rootless containers)")
	flag.Parse()

	// Download and prepare the container rootfs
	root := rootfs(*img)

	// Use clone approach - create child process with new namespaces
	fmt.Println("Using clone approach for namespace creation")
	
	// Create the child process that will run in new namespaces
	cmd := exec.Command("/proc/self/exe", "--child", root)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	// Configure the namespaces to create
	// - CLONE_NEWNS: New mount namespace (isolated filesystem view)
	// - CLONE_NEWUTS: New UTS namespace (isolated hostname)
	// - CLONE_NEWPID: New PID namespace (isolated process tree)
	flags := syscall.CLONE_NEWNS | syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID
	if *userns {
		flags |= syscall.CLONE_NEWUSER
	}

	sysProcAttr := &syscall.SysProcAttr{
		Cloneflags:   uintptr(flags),
		Unshareflags: syscall.CLONE_NEWNS, // Make mount tree totally private for security
	}

	// Only set UID/GID mappings when using user namespace and not running as root
	// Root doesn't need mappings as it already has all privileges
	if *userns && os.Getuid() != 0 {
		sysProcAttr.UidMappings = []syscall.SysProcIDMap{{
			HostID: os.Getuid(), ContainerID: 0, Size: 1,
		}}
		sysProcAttr.GidMappings = []syscall.SysProcIDMap{{
			HostID: os.Getgid(), ContainerID: 0, Size: 1,
		}}
	}

	cmd.SysProcAttr = sysProcAttr

	// Start the container
	must(cmd.Run())
}
