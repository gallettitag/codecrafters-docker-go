//go:build linux
// +build linux

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

const (
	getTokenURL       = "https://auth.docker.io/token?service=registry.docker.io&scope=repository:%s:pull"
	getManifestURL    = "https://registry.hub.docker.com/v2/library/%s/manifests/%s"
	getLayerURL       = "https://registry.hub.docker.com/v2/library/%s/blobs/%s"
	contentTypeHeader = "application/vnd.docker.distribution.manifest.v2+json"
)

type TokenResponse struct {
	Token     string    `json:"token"`
	AuthToken string    `json:"access_token"`
	ExpiresIn int       `json:"expires_in"`
	IssuedAt  time.Time `json:"issued_at"`
}

type ManifestResponse struct {
	SchemaVersion int    `json:"schemaVersion"`
	MediaType     string `json:"mediaType"`
	Config        struct {
		MediaType string `json:"mediaType"`
		Size      int    `json:"size"`
		Digest    string `json:"digest"`
	} `json:"config"`
	Layers []struct {
		MediaType string `json:"mediaType"`
		Size      int    `json:"size"`
		Digest    string `json:"digest"`
	} `json:"layers"`
}

// Usage: your_docker.sh run <image> <command> <arg1> <arg2> ...
func main() {
	imageName := os.Args[2]
	command := os.Args[3]
	args := os.Args[4:len(os.Args)]

	dir, err := os.MkdirTemp("", "my-docker")
	if err != nil {
		handleError(err)
	}

	defer os.RemoveAll(dir)

	image, tag := parseImage(imageName)

	token, err := getToken(fmt.Sprintf("library/%s", image))
	if err != nil {
		handleError(err)
	}

	manifest, err := getManifest(image, token, tag)
	if err != nil {
		handleError(err)
	}

	var layerNames []string
	for _, manifest := range manifest.Layers {
		layer, err := pullLayers(image, token, manifest.Digest)
		if err != nil {
			handleError(err)
		}

		layerNames = append(layerNames, layer)
	}

	for _, layer := range layerNames {
		err = extractTar(layer, dir)
		if err != nil {
			handleError(err)
		}
	}

	err = createFileSystem(dir)
	if err != nil {
		handleError(err)
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

func extractTar(src, dest string) error {
	cmd := exec.Command("tar", "-xzf", src, "-C", dest)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		return err
	}

	return nil
}

func pullLayers(image, token, digest string) (string, error) {
	resp, err := downloadLayer(image, token, digest)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("error downloading layer (status: %s)", resp.Status)
	}

	return saveLayerToFile(resp.Body, digest)
}

func saveLayerToFile(body io.ReadCloser, digest string) (string, error) {
	filename := fmt.Sprintf("%s.tar.gz", digest[7:])
	layerFile, err := os.Create(filename)
	if err != nil {
		return "", fmt.Errorf("error creating file %s: %w", filename, err)
	}
	defer layerFile.Close()

	_, err = io.Copy(layerFile, body)
	if err != nil {
		return "", fmt.Errorf("error writing file %s: %w", filename, err)
	}

	return filename, nil
}

func downloadLayer(image, token, digest string) (*http.Response, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf(getLayerURL, image, digest), nil)
	if err != nil {
		return nil, fmt.Errorf("error creatign request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	return httpClient.Do(req)
}

func getManifest(image, token, tag string) (*ManifestResponse, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf(getManifestURL, image, tag), nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Accept", contentTypeHeader)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("error getting manifest. Status: %s", resp.Status)
	}
	defer resp.Body.Close()

	var manifest *ManifestResponse
	err = json.NewDecoder(resp.Body).Decode(&manifest)
	if err != nil {
		return nil, err
	}

	return manifest, nil
}

func getToken(image string) (string, error) {
	resp, err := httpClient.Get(fmt.Sprintf(getTokenURL, image))
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("error getting token. status: %s", resp.Status)
	}
	defer resp.Body.Close()
	var token TokenResponse
	err = json.NewDecoder(resp.Body).Decode(&token)
	if err != nil {
		return "", err
	}

	return token.Token, nil
}

func parseImage(arg string) (string, string) {
	parts := strings.Split(arg, ":")

	if (len(parts)) == 1 {
		return parts[0], "latest"
	}

	return parts[0], parts[1]
}

func handleError(err error) {
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		os.Exit(exitError.ExitCode())
	} else {
		fmt.Printf("Error %v\n", err)
		os.Exit(1)
	}
}

func createFileSystem(dir string) error {
	err := syscall.Chroot(dir)
	if err != nil {
		return err
	}

	return nil
}
