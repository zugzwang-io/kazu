// Package imagetest runs a service's real image for a test: a small,
// standard-library take on testcontainers, driving the docker CLI.
//
// Image tests carry the `image` build tag, so plain `go test ./...` needs no
// Docker: run them with `go test -tags image ./...`.
package imagetest

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Container is a running service image, reachable from the test at URL.
type Container struct {
	Name string
	URL  string // http://127.0.0.1:<random host port>
}

// Start builds services/<service>/Dockerfile, runs the image with port
// published on a random host port, and waits until GET /healthz answers 2xx.
// The container is removed when the test ends; if the test failed, its logs
// are printed first.
func Start(t *testing.T, service string, port string, env ...string) *Container {
	t.Helper()
	image := "shop-" + service + ":test"
	docker(t, "build", "-q", "-f", filepath.Join(shopRoot(), "services", service, "Dockerfile"), "-t", image, shopRoot())

	b := make([]byte, 4)
	_, _ = rand.Read(b)
	c := &Container{Name: "imagetest-" + service + "-" + hex.EncodeToString(b)}
	args := []string{"run", "-d", "--name", c.Name, "-p", "127.0.0.1::" + port}
	for _, e := range env {
		args = append(args, "-e", e)
	}
	docker(t, append(args, image)...)
	t.Cleanup(func() {
		if t.Failed() {
			out, _ := exec.Command("docker", "logs", c.Name).CombinedOutput()
			t.Logf("%s logs:\n%s", c.Name, out)
		}
		_ = exec.Command("docker", "rm", "-f", c.Name).Run()
	})

	// "127.0.0.1:49153", possibly followed by an IPv6 line.
	hostPort, _, _ := strings.Cut(docker(t, "port", c.Name, port+"/tcp"), "\n")
	c.URL = "http://" + hostPort
	c.waitHealthy(t, 60*time.Second)
	return c
}

func (c *Container) waitHealthy(t *testing.T, timeout time.Duration) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(timeout)
	for {
		resp, err := client.Get(c.URL + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode/100 == 2 {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s not healthy after %v: last error %v", c.Name, timeout, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func docker(t *testing.T, args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	cmd := exec.Command("docker", args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("docker %s: %v\n%s", args[0], err, errb.String())
	}
	return strings.TrimSpace(out.String())
}

// shopRoot is examples/shop, the build context for every service image.
func shopRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}
