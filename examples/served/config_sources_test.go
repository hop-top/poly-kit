package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests in this file run the built binary, because the question
// they answer is what an operator gets: whether a services.* key set
// in the environment, with -c, or in the tool's config file reaches
// the served services, with nothing wired for it in main.

var (
	binOnce sync.Once
	binPath string
	binErr  error
	binDir  string
)

func TestMain(m *testing.M) {
	code := m.Run()
	if binDir != "" {
		_ = os.RemoveAll(binDir)
	}
	os.Exit(code)
}

// servedBinary builds this package once per test run.
func servedBinary(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		binDir, binErr = os.MkdirTemp("", "served-bin")
		if binErr != nil {
			return
		}
		binPath = filepath.Join(binDir, "served")
		out, err := exec.Command("go", "build", "-buildvcs=false", "-o", binPath, ".").CombinedOutput()
		if err != nil {
			binErr = fmt.Errorf("go build: %w\n%s", err, out)
		}
	})
	require.NoError(t, binErr)
	return binPath
}

// binRun is one run of the built binary.
type binRun struct {
	cmd    *exec.Cmd
	stderr strings.Builder
	mu     sync.Mutex
	done   chan struct{}
	err    error
	apiURL string
	ready  bool
}

var apiAddrRE = regexp.MustCompile(`ready_reported .*service=api address=(\S+)`)

// runBinary starts the binary with an isolated home, the given
// environment and arguments, and waits until the supervisor reports
// ready or the process exits. A ready run is interrupted at cleanup.
func runBinary(t *testing.T, home string, env []string, args ...string) *binRun {
	t.Helper()
	cmd := exec.Command(servedBinary(t), args...)
	cmd.Env = append([]string{
		"PATH=/usr/bin:/bin",
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"XDG_STATE_HOME=" + filepath.Join(home, ".state"),
		"XDG_DATA_HOME=" + filepath.Join(home, ".data"),
		"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		"XDG_RUNTIME_DIR=" + filepath.Join(home, "run"),
	}, env...)
	cmd.Dir = home
	pipe, err := cmd.StderrPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())

	run := &binRun{cmd: cmd, done: make(chan struct{})}
	readyCh := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(pipe)
		signaled := false
		for sc.Scan() {
			line := sc.Text()
			run.mu.Lock()
			run.stderr.WriteString(line + "\n")
			if m := apiAddrRE.FindStringSubmatch(line); m != nil {
				run.apiURL = "http://" + m[1]
			}
			run.mu.Unlock()
			if !signaled && strings.Contains(line, "ready_reported object=supervisor") {
				signaled = true
				close(readyCh)
			}
		}
		_, _ = io.Copy(io.Discard, pipe)
		run.err = cmd.Wait()
		close(run.done)
	}()

	select {
	case <-readyCh:
		run.ready = true
		t.Cleanup(func() {
			_ = cmd.Process.Signal(os.Interrupt)
			select {
			case <-run.done:
			case <-time.After(10 * time.Second):
				_ = cmd.Process.Kill()
				<-run.done
			}
		})
	case <-run.done:
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		<-run.done
		t.Fatalf("served %v neither became ready nor exited:\n%s", args, run.output())
	}
	return run
}

func (r *binRun) output() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stderr.String()
}

// exitCode is the exit code of a run that ended, or -1.
func (r *binRun) exitCode() int {
	var ee *exec.ExitError
	if errors.As(r.err, &ee) {
		return ee.ExitCode()
	}
	if r.err == nil && !r.ready {
		return 0
	}
	return -1
}

// newHome is an isolated home with an optional user config file.
func newHome(t *testing.T, configYAML string) string {
	t.Helper()
	home, err := os.MkdirTemp("", "sv")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	require.NoError(t, os.MkdirAll(filepath.Join(home, "run"), 0o700))
	if configYAML != "" {
		dir := filepath.Join(home, ".config", "served")
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(configYAML), 0o600))
	}
	return home
}

// extraFile writes a file for -c <path>.
func extraFile(t *testing.T, home, yaml string) string {
	t.Helper()
	p := filepath.Join(home, "extra.yaml")
	require.NoError(t, os.WriteFile(p, []byte(yaml), 0o600))
	return p
}

// A documented key the api reads (services.api.addr) reaches it from
// every source: a non-loopback address with no authentication is
// refused at exit 2, which proves the key was read.
func TestBinaryServicesAddrFromEverySource(t *testing.T) {
	const remote = "0.0.0.0:0"
	fileYAML := "services:\n  api:\n    addr: " + remote + "\n"
	cases := map[string]func(t *testing.T) *binRun{
		"environment": func(t *testing.T) *binRun {
			return runBinary(t, newHome(t, ""), []string{"SERVED_SERVICES_API_ADDR=" + remote}, "serve", "api")
		},
		"-c key=value": func(t *testing.T) *binRun {
			return runBinary(t, newHome(t, ""), nil, "-c", "services.api.addr="+remote, "serve", "api")
		},
		"user config file": func(t *testing.T) *binRun {
			return runBinary(t, newHome(t, fileYAML), nil, "serve", "api")
		},
		"-c path": func(t *testing.T) *binRun {
			home := newHome(t, "")
			return runBinary(t, home, nil, "-c", extraFile(t, home, fileYAML), "serve", "api")
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			r := run(t)
			require.False(t, r.ready, "the key was ignored: served on the default address\n%s", r.output())
			assert.Equal(t, 2, r.exitCode(), r.output())
			assert.Contains(t, r.output(), `"0.0.0.0:0" is not a loopback address and the api service has no authentication`)
		})
	}
}

// The mcp and rpc services' own keys reach them from the environment
// like the api's: <TOOL>_SERVICES_MCP_ADDR and _RPC_ADDR name the
// service, and a non-loopback address with no authentication is
// refused at exit 2, which proves the key was read. The same variable
// set to loopback serves.
func TestBinaryServicesMCPAndRPCKeysFromTheEnvironment(t *testing.T) {
	for _, svc := range []string{"mcp", "rpc"} {
		t.Run(svc, func(t *testing.T) {
			env := "SERVED_SERVICES_" + strings.ToUpper(svc) + "_ADDR="
			r := runBinary(t, newHome(t, ""), []string{env + "0.0.0.0:0"}, "serve", svc)
			require.False(t, r.ready, "the key was ignored: served on the default address\n%s", r.output())
			assert.Equal(t, 2, r.exitCode(), r.output())
			assert.Contains(t, r.output(), `"0.0.0.0:0" is not a loopback address and the `+svc+` service has no authentication`)

			r = runBinary(t, newHome(t, ""), []string{env + "127.0.0.1:0"}, "serve", svc)
			require.True(t, r.ready, r.output())
		})
	}
}

// services.api.insecure_remote, the documented opt-in, is read from
// the environment and -c: the refusal moves on to the policy gate.
func TestBinaryServicesInsecureRemote(t *testing.T) {
	for name, env := range map[string][]string{
		"environment": {"SERVED_SERVICES_API_INSECURE_REMOTE=true"},
		"-c":          nil,
	} {
		t.Run(name, func(t *testing.T) {
			args := []string{"serve", "api", "--addr", "0.0.0.0:0"}
			if env == nil {
				args = append([]string{"-c", "services.api.insecure_remote=true"}, args...)
			}
			r := runBinary(t, newHome(t, ""), env, args...)
			require.False(t, r.ready, r.output())
			assert.Equal(t, 2, r.exitCode(), r.output())
			assert.NotContains(t, r.output(), "no authentication", "the opt-in was read")
			assert.Contains(t, r.output(), "no delegation policy is configured")
		})
	}
}

// A middleware key (services.api.body_limit.max_bytes) caps request
// bodies whichever source sets it.
func TestBinaryServicesBodyLimitFromEverySource(t *testing.T) {
	fileYAML := "services:\n  api:\n    body_limit:\n      max_bytes: 64\n"
	cases := map[string]func(t *testing.T) *binRun{
		"environment": func(t *testing.T) *binRun {
			return runBinary(t, newHome(t, ""), []string{"SERVED_SERVICES_API_BODY_LIMIT_MAX_BYTES=64"},
				"serve", "api", "--addr", "127.0.0.1:0")
		},
		"services.all from the environment": func(t *testing.T) *binRun {
			return runBinary(t, newHome(t, ""), []string{"SERVED_SERVICES_ALL_BODY_LIMIT_MAX_BYTES=64"},
				"serve", "api", "--addr", "127.0.0.1:0")
		},
		"-c key=value": func(t *testing.T) *binRun {
			return runBinary(t, newHome(t, ""), nil,
				"-c", "services.api.body_limit.max_bytes=64", "serve", "api", "--addr", "127.0.0.1:0")
		},
		"user config file": func(t *testing.T) *binRun {
			return runBinary(t, newHome(t, fileYAML), nil, "serve", "api", "--addr", "127.0.0.1:0")
		},
	}
	body := `{"args":["` + strings.Repeat("x", 200) + `"]}`
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			r := run(t)
			require.True(t, r.ready, r.output())
			status, got := httpDo(t, http.MethodPost, r.apiURL+"/v1/commands/item/add", body)
			assert.Equal(t, http.StatusRequestEntityTooLarge, status, string(got))
		})
	}

	t.Run("the service's key from the file beats services.all from -c", func(t *testing.T) {
		home := newHome(t, "services:\n  api:\n    body_limit:\n      max_bytes: 100000\n")
		r := runBinary(t, home, nil, "-c", "services.all.body_limit.max_bytes=64",
			"serve", "api", "--addr", "127.0.0.1:0")
		require.True(t, r.ready, r.output())
		status, got := httpDo(t, http.MethodPost, r.apiURL+"/v1/commands/item/add", body)
		assert.Equal(t, http.StatusOK, status, string(got))
	})
}

// Precedence across sources: the flag, then the environment, then the
// config file.
func TestBinaryServicesPrecedence(t *testing.T) {
	t.Run("--addr beats the environment", func(t *testing.T) {
		r := runBinary(t, newHome(t, ""), []string{"SERVED_SERVICES_API_ADDR=0.0.0.0:0"},
			"serve", "api", "--addr", "127.0.0.1:0")
		require.True(t, r.ready, r.output())
		assert.Contains(t, r.apiURL, "127.0.0.1:")
	})
	t.Run("the environment beats the config file", func(t *testing.T) {
		r := runBinary(t, newHome(t, "services:\n  api:\n    addr: 0.0.0.0:0\n"),
			[]string{"SERVED_SERVICES_API_ADDR=127.0.0.1:0"}, "serve", "api")
		require.True(t, r.ready, r.output())
		assert.Contains(t, r.apiURL, "127.0.0.1:")
	})
	t.Run("-c beats the environment", func(t *testing.T) {
		r := runBinary(t, newHome(t, ""), []string{"SERVED_SERVICES_API_ADDR=0.0.0.0:0"},
			"-c", "services.api.addr=127.0.0.1:0", "serve", "api")
		require.True(t, r.ready, r.output())
		assert.Contains(t, r.apiURL, "127.0.0.1:")
	})
}

// A service configured only by the environment is configured: the
// supervisor form starts it.
func TestBinaryServicesEnableFromTheEnvironment(t *testing.T) {
	home := newHome(t, "")
	sock := filepath.Join(home, "s.sock")
	r := runBinary(t, home, []string{
		"SERVED_SERVICES_API_ENABLED=false",
		"SERVED_SERVICES_SOCKET_ENABLED=true",
		"SERVED_SERVICES_SOCKET_PATH=" + sock,
	}, "serve")
	require.True(t, r.ready, r.output())
	assert.Contains(t, r.output(), "service=socket")
	assert.NotContains(t, r.output(), "service=api")
	_, err := os.Stat(sock)
	assert.NoError(t, err)
}

// Unknown keys are refused at exit 2 from every source, naming the key.
func TestBinaryServicesUnknownKeysRefused(t *testing.T) {
	cases := map[string]struct {
		env  []string
		args []string
		file string
		want string
	}{
		"environment, misspelled block key": {
			env:  []string{"SERVED_SERVICES_API_BODY_LIMIT_MAXBYTES=5"},
			want: `services.api.body_limit.maxbytes: unknown key "maxbytes"`,
		},
		"-c, lifecycle key under services.all": {
			args: []string{"-c", "services.all.enabled=true"},
			want: "services.all.enabled: not a middleware key",
		},
		"-c, a service's own key under services.all": {
			args: []string{"-c", "services.all.path=nope"},
			want: "services.all.path: not a middleware key",
		},
		"config file, unknown key in services.all": {
			file: "services:\n  all:\n    health:\n      prefix: /x\n",
			want: `services.all.health.prefix: unknown key "prefix"`,
		},
		"-c, bad value": {
			args: []string{"-c", "services.api.body_limit.max_bytes=-5"},
			want: "services.api.body_limit.max_bytes",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			args := append(append([]string{}, c.args...), "serve", "api", "--addr", "127.0.0.1:0")
			r := runBinary(t, newHome(t, c.file), c.env, args...)
			require.False(t, r.ready, "served with a bad key\n%s", r.output())
			assert.Equal(t, 2, r.exitCode(), r.output())
			assert.Contains(t, r.output(), c.want)
		})
	}
}

// The metrics scrape sub-block (services.<svc>.metrics.scrape) is read
// from the environment and a config file like any other block: the
// endpoint serves, and beyond loopback it is refused without its own
// opt-in.
func TestBinaryServicesMetricsScrape(t *testing.T) {
	env := []string{
		"SERVED_SERVICES_API_METRICS_ENABLED=true",
		"SERVED_SERVICES_API_METRICS_EXPORTER=none",
		"SERVED_SERVICES_API_METRICS_SCRAPE_ENABLED=true",
	}
	t.Run("environment serves the endpoint", func(t *testing.T) {
		r := runBinary(t, newHome(t, ""), env, "serve", "api", "--addr", "127.0.0.1:0")
		require.True(t, r.ready, r.output())
		status, body := httpDo(t, http.MethodGet, r.apiURL+"/metrics", "")
		assert.Equal(t, http.StatusOK, status, string(body))
	})
	t.Run("config file sets the path", func(t *testing.T) {
		home := newHome(t, "services:\n  all:\n    metrics:\n      scrape:\n        path: /_kit/metrics\n")
		r := runBinary(t, home, env, "serve", "api", "--addr", "127.0.0.1:0")
		require.True(t, r.ready, r.output())
		status, body := httpDo(t, http.MethodGet, r.apiURL+"/_kit/metrics", "")
		assert.Equal(t, http.StatusOK, status, string(body))
	})
	t.Run("beyond loopback without allow_remote is refused", func(t *testing.T) {
		r := runBinary(t, newHome(t, ""), env,
			"serve", "api", "--addr", "0.0.0.0:0", "--insecure-remote", "--insecure-no-policy")
		require.False(t, r.ready, r.output())
		assert.Equal(t, 2, r.exitCode(), r.output())
		assert.Contains(t, r.output(), "services.api.metrics.scrape.enabled")
	})
	t.Run("-c, unknown scrape key is refused", func(t *testing.T) {
		r := runBinary(t, newHome(t, ""), env,
			"-c", "services.api.metrics.scrape.allow_remot=true", "serve", "api", "--addr", "127.0.0.1:0")
		require.False(t, r.ready, r.output())
		assert.Equal(t, 2, r.exitCode(), r.output())
		assert.Contains(t, r.output(), `services.api.metrics.scrape.allow_remot: unknown key "allow_remot"`)
	})
}
