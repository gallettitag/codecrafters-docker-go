//go:build linux
// +build linux

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// Usage: your_docker.sh run <image> <command> <arg1> <arg2> ...
func main() {
	command := os.Args[3]
	args := os.Args[4:len(os.Args)]

	err := createFileSystem(command)
	if err != nil {
		handleError(fmt.Errorf("Error creating file system %v", err))
	}

	cmd := exec.Command(command, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWPID,
	}
	err = cmd.Run()
	if err != nil {
		handleError(err)
	}
}

func handleError(err error) {
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		os.Exit(exitError.ExitCode())
	} else {
		fmt.Errorf("Error %v\n", err)
		os.Exit(1)
	}
}

func createFileSystem(path string) error {
	dir, err := os.MkdirTemp("", "my-docker")
	if err != nil {
		return fmt.Errorf("Error Creating temp dir %v", err)
	}

	defer os.RemoveAll(dir)
	destinationPath := filepath.Join(dir, path)
	err = os.MkdirAll(filepath.Dir(destinationPath), 0660)
	if err != nil {
		return fmt.Errorf("Error populating file system %v", err)
	}

	binary, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("Error creating binary %v", err)
	}
	defer binary.Close()

	dest, err := os.Create(destinationPath)
	if err != nil {
		return fmt.Errorf("Error creating binary path %v", err)
	}
	defer dest.Close()

	err = dest.Chmod(0100)
	if err != nil {
		return fmt.Errorf("Error setting chmod perms %v", err)
	}

	_, err = io.Copy(dest, binary)
	if err != nil {
		return fmt.Errorf("Error copying binary %v", err)
	}

	err = syscall.Chroot(dir)
	if err != nil {
		return fmt.Errorf("Error setting chroot %v", err)
	}

	return nil
}
