package main

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestMain builds the binary once for the end-to-end tests.
var binPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "vastai-mcp-e2e")
	if err != nil {
		panic(err)
	}
	binPath = filepath.Join(dir, "vastai-mcp")
	if out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput(); err != nil {
		panic(string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func freePort(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func runBin(t *testing.T, env []string, args ...string) (*exec.Cmd, *bufio.Reader) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = t.TempDir() // no .env
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "VASTAI_API_KEY=test"}, env...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	return cmd, bufio.NewReader(stderr)
}

func waitListening(t *testing.T, r *bufio.Reader) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		line, err := r.ReadString('\n')
		if strings.Contains(line, "listening on") {
			return
		}
		if err != nil {
			t.Fatalf("server exited before listening: %s", line)
		}
	}
	t.Fatal("timeout waiting for listen")
}

func initReq(t *testing.T, url, token string) int {
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`
	req, _ := http.NewRequest("POST", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestNonLoopbackRefusedWithoutToken(t *testing.T) {
	cmd := exec.Command(binPath, "-http", ":0")
	cmd.Env = []string{"VASTAI_API_KEY=test", "HOME=" + t.TempDir()}
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "non-loopback") {
		t.Fatalf("expected refusal, got err=%v out=%s", err, out)
	}
}

func TestLoopbackNoTokenServes(t *testing.T) {
	addr := freePort(t)
	_, r := runBin(t, nil, "-http", addr)
	waitListening(t, r)
	if code := initReq(t, "http://"+addr+"/", ""); code != 200 {
		t.Fatalf("loopback init = %d", code)
	}
}

func TestInsecureRemoteRequiresToken(t *testing.T) {
	addr := freePort(t) // 127.0.0.1 is loopback; use 0.0.0.0 with the same port for a non-loopback bind
	_, port, _ := net.SplitHostPort(addr)
	cmd, r := runBin(t, []string{"VASTAI_MCP_TOKEN=s3cret"}, "-http", "0.0.0.0:"+port, "-insecure-http")
	waitListening(t, r)
	url := "http://127.0.0.1:" + port + "/"
	if code := initReq(t, url, ""); code != 401 {
		t.Errorf("no token = %d, want 401", code)
	}
	if code := initReq(t, url, "wrong"); code != 401 {
		t.Errorf("wrong token = %d, want 401", code)
	}
	if code := initReq(t, url, "s3cret"); code != 200 {
		t.Errorf("right token = %d, want 200", code)
	}
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(cmd.Process.Pid) + "/cmdline"); err == nil && strings.Contains(string(b), "s3cret") {
		t.Error("token visible in argv")
	}
	// Graceful shutdown on SIGTERM.
	cmd.Process.Signal(os.Interrupt)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("exit on SIGINT: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("did not shut down")
	}
}

func TestStdioExitsCleanOnEOF(t *testing.T) {
	cmd := exec.Command(binPath)
	cmd.Env = []string{"VASTAI_API_KEY=test", "HOME=" + t.TempDir()}
	cmd.Dir = t.TempDir()
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stdin.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}` + "\n"))
	line, _ := bufio.NewReader(stdout).ReadString('\n')
	var resp map[string]any
	if json.Unmarshal([]byte(line), &resp) != nil || resp["result"] == nil {
		t.Fatalf("bad initialize response: %s", line)
	}
	stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("stdio EOF should exit 0: %v", err)
	}
}

func TestMalformedGuardrailEnvRefusesStart(t *testing.T) {
	for _, kv := range []string{"VASTAI_MAX_DPH=$0.50", "VASTAI_MAX_INSTANCES=ten", "VASTAI_READ_ONLY=maybe", "VASTAI_CONFIRM=nah", "VASTAI_MAX_DPH=-1"} {
		cmd := exec.Command(binPath, "-version")
		cmd.Env = []string{"VASTAI_API_KEY=test", "HOME=" + t.TempDir(), kv}
		cmd.Dir = t.TempDir()
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "is not") {
			t.Errorf("%s: expected refusal, got err=%v out=%s", kv, err, out)
		}
	}
	cmd := exec.Command(binPath, "-version")
	cmd.Env = []string{"VASTAI_API_KEY=test", "HOME=" + t.TempDir(), "VASTAI_READ_ONLY=yes", "VASTAI_MAX_DPH=0.5"}
	cmd.Dir = t.TempDir()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("valid env refused: %s", out)
	}
}
