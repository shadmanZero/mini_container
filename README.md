# Container Runtime in Go - Educational Blog Post

> **Note: This is educational content from a blog post about container internals. This is not a production project to build, but rather a learning resource to understand how containers work under the hood.**

A minimal container runtime implementation in Go that demonstrates the core concepts behind containerization. This educational content walks through building a basic container runtime from scratch using Linux namespaces, similar to how Docker works under the hood.

## 🎯 What We're Learning About

This educational content explores how a simple container runtime works:
- Pull OCI-compliant images (like those from Docker Hub)
- Create isolated environments using Linux namespaces
- Run containers with proper filesystem, process, and hostname isolation
- Provide an interactive shell inside containers

**Example Usage:**
```bash
sudo ./container-go --image=alpine:latest
# You'll get an isolated Alpine Linux environment!
```

## 🚀 Following Along

### Prerequisites (if you want to test the concepts)
- Linux system (required for namespaces)
- Go 1.19+ installed
- Root privileges (for namespace operations)

### Code Examples
The blog post includes complete, working code examples that you can run to understand container concepts:

```bash
# If you want to experiment with the code examples:
go mod tidy
go build -o container-go main.go
sudo ./container-go --image=alpine:latest
```

You should see something like:
```bash
Downloading and unpacking alpine:latest to /tmp/rootfs-1234567890
Using clone approach for namespace creation

/ # echo $$
1                        # You're PID 1 in the container!
/ # hostname  
shadman-lab             # Isolated hostname
/ # exit
```

## 🧠 Understanding Containers: The Foundation

Before diving into the code, it's crucial to understand what containers actually are and how they achieve isolation.

### What Are Namespaces?

*"I want each process to live in its own little world."*

Imagine your computer's operating system as a giant mansion with all the system resources—filesystem, running processes, network connections. If every program runs loose in this mansion, chaos ensues. One program might mess with another's files or interfere with system processes.

A **namespace** is like giving each program its own magical, self-contained apartment within the mansion. When a program enters its apartment, it's completely isolated from everything else.

There are several types of namespaces:
- **Mount** (`CLONE_NEWNS`): Isolated filesystem view
- **UTS** (`CLONE_NEWUTS`): Isolated hostname and domain name  
- **PID** (`CLONE_NEWPID`): Isolated process tree (your process becomes PID 1)
- **Network** (`CLONE_NEWNET`): Isolated network stack
- **User** (`CLONE_NEWUSER`): Isolated user and group IDs

### Testing Namespaces with Shell Commands

Let's see namespaces in action using standard Linux tools:

#### Mount Namespace Example
```bash
sudo unshare --mount --fork bash -c '
  echo "*** Inside mount namespace: creating a private tmpfs ***"
  mount -t tmpfs tmpfs /mnt          # This new mount is only visible here
  touch /mnt/hello && ls -l /mnt
  read -p "Press Enter to exit..."
'
echo "*** Back on host: trying to access /mnt/hello (should fail) ***"
ls /mnt/hello || echo "Host sees nothing, as expected."
```

#### UTS Namespace Example
```bash
sudo unshare --uts --fork bash -c '
  hostname shadmanHehe
  echo "Inside UTS namespace, the hostname is: $(hostname)"
'
echo "Back on the host, the hostname is: $(hostname)"
```

These examples demonstrate the core principle: **actions inside a namespace remain isolated and don't affect the host system**.

## 🔧 Implementation Deep Dive

### Basic Namespace Creation in Go

Here's our first Go implementation that creates isolated namespaces:

```go
//go:build linux

package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"syscall"
	"golang.org/x/sys/unix"
)

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func child() {
	fmt.Println("inside child namespace")
	must(unix.Sethostname([]byte("shadmanHehe")))
	
	// Mount a temporary filesystem
	mnt := "/mnt"
	must(unix.Mount("tmpfs", mnt, "tmpfs", 0, ""))
	fmt.Printf("mounted tmpfs at %s\n", mnt)

	// Create a test file
	f, err := os.Create("/mnt/hello")
	must(err)
	f.Close()
	
	fmt.Print("Press ENTER to exit child…")
	bufio.NewReader(os.Stdin).ReadBytes('\n')
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "child" {
		child()
		return
	}
	
	// Create child process with new namespaces
	cmd := exec.Command("/proc/self/exe", "child")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:   syscall.CLONE_NEWNS | syscall.CLONE_NEWUTS,
		Unshareflags: syscall.CLONE_NEWNS, // Extra security
	}
	
	must(cmd.Run())
	
	// Verify isolation
	if _, err := os.Stat("/mnt/hello"); os.IsNotExist(err) {
		fmt.Println("host sees nothing — GOOD")
	}
}
```

### How the Clone Approach Works

Our container runtime uses the **clone approach** rather than unshare:

1. **Parent Process**: Downloads the container image and prepares the environment
2. **Child Creation**: Uses `exec.Command` with special flags to create an isolated child process
3. **Namespace Flags**: 
   - `CLONE_NEWNS`: New mount namespace (filesystem isolation)
   - `CLONE_NEWUTS`: New UTS namespace (hostname isolation)  
   - `CLONE_NEWPID`: New PID namespace (process tree isolation)
   - `CLONE_NEWUSER`: New user namespace (optional, for rootless containers)

4. **Security Enhancement**: `Unshareflags: syscall.CLONE_NEWNS` ensures mount operations never propagate back to the host

### Advanced Isolation: Complete Environment

The simple example above still shares the host's filesystem and process tree. For true container isolation, we need:

```go
func child(rootfs string) {
	// Set container hostname
	must(unix.Sethostname([]byte("shadman-lab")))

	// Mount /proc before chroot so it's available inside the container
	procPath := filepath.Join(rootfs, "proc")
	must(os.MkdirAll(procPath, 0755))
	must(unix.Mount("proc", procPath, "proc",
		uintptr(unix.MS_NOSUID|unix.MS_NOEXEC|unix.MS_NODEV), ""))

	// Change root filesystem to the container's rootfs
	must(unix.Chroot(rootfs))
	must(unix.Chdir("/"))

	// Execute shell as PID 1 inside container
	must(syscall.Exec("/bin/sh", []string{"sh", "-i"}, os.Environ()))
}
```

## 📦 What is an OCI Image?

Think of an OCI (Open Container Initiative) image as a "some-assembly-required" furniture kit:

- **The Kit (The Image)**: Contains all necessary files, libraries, and binaries, plus instructions (`config.json`). Crucially, it **does NOT** include a Linux kernel—only the application's root filesystem.

- **The Workshop (Host OS)**: Your host operating system provides the essential engine: the **Linux kernel**.

- **Assembly (Container Runtime)**: Our Go program reads the instructions, assembles the filesystem layers in isolated namespaces, and uses the host kernel to run the application.

### How Does It Boot Without a Kernel?

**It doesn't!** This is the key insight. A container **borrows the kernel of its host machine**.

The isolation features (namespaces, chroot) trick the process into thinking it has its own private machine. In reality, it's just a standard Linux process with a restricted view of the system. This is why containers are lightweight and fast—we're not booting a new OS, just starting an isolated process.

## 🏗️ Full Container Runtime

The complete implementation includes:

### Image Handling
- **Download**: Pull OCI images from registries (Docker Hub, etc.)
- **Unpack**: Extract layered filesystem into a temporary directory
- **Prepare**: Set up the root filesystem for the container

### Process Isolation  
- **PID Namespace**: Container processes get their own process tree (PID 1)
- **Mount Namespace**: Isolated filesystem view
- **UTS Namespace**: Custom hostname
- **User Namespace**: Optional rootless container support

### Security Features
- **Private Mount Tree**: `Unshareflags` prevents mount propagation to host
- **Restricted Proc**: Mount `/proc` with security flags
- **Chroot Jail**: Container can't access host filesystem

## 🛠️ Command Line Options

```bash
./container-go [OPTIONS]

Options:
  --image string    OCI image reference (default "alpine:latest")
  --userns         Enable user namespace for rootless containers
```

## 🔍 Example Output

```bash
$ sudo ./container-go --image=alpine:latest
Downloading and unpacking alpine:latest to /tmp/rootfs-1641234567890
Using clone approach for namespace creation

/ # echo $$
1                        # Proves shell is PID 1 in PID namespace
/ # hostname
shadman-lab             # UTS namespace hostname we set in code  
/ # cat /etc/os-release | head -3
NAME="Alpine Linux"
ID=alpine
VERSION_ID=3.20.0
/ # ls /
bin    dev    etc    home   proc   root   tmp    usr    var
/ # exit                # Ctrl-D or type exit
$
```

## 🧪 Testing and Development

### Prerequisites Check
```bash
# Verify you're on Linux
uname -s  # Should output: Linux

# Check namespace support
ls /proc/self/ns/
# Should show: mnt, pid, uts, etc.

# Verify Go installation  
go version  # Should be 1.19+
```

### Build and Test
```bash
# Install dependencies
go mod tidy

# Build the runtime
go build -o container-go main.go

# Test with different images
sudo ./container-go --image=ubuntu:latest
sudo ./container-go --image=debian:latest
sudo ./container-go --image=alpine:3.18
```

## 🤝 About This Educational Content

This is educational content from a blog post demonstrating container internals. The concepts covered include:
- Linux namespaces and process isolation
- OCI image format and container filesystems
- Container runtime implementation patterns
- Clone vs unshare approaches for namespace creation
- Security considerations in containerization

## 📚 Further Reading

- [Linux Namespaces Documentation](https://man7.org/linux/man-pages/man7/namespaces.7.html)
- [OCI Image Specification](https://github.com/opencontainers/image-spec)
- [How Container Runtimes Work](https://www.ianlewis.org/en/container-runtimes-part-1-introduction-container-r)
- [Building a Container Runtime](https://www.youtube.com/watch?v=8fi7uSYlOdc)

## ⚖️ License

MIT License - See LICENSE file for details.

---

**Educational Note**: This content is from a blog post about container internals and is purely for educational purposes. The code examples demonstrate concepts but are not intended for production use. For production container runtimes, use mature solutions like containerd, CRI-O, or runc.