package ssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cryptossh "golang.org/x/crypto/ssh"
)

// Each server accepts one loopback connection and joins it during cleanup.
func serveTCP(t *testing.T, handle func(net.Conn)) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var connection net.Conn
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		mu.Lock()
		connection = conn
		mu.Unlock()
		defer conn.Close()
		handle(conn)
	}()
	t.Cleanup(func() {
		listener.Close()
		mu.Lock()
		if connection != nil {
			connection.Close()
		}
		mu.Unlock()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("SSH server did not stop")
		}
	})
	return listener.Addr().String()
}

func serveSSH(t *testing.T, password string, handle func(*cryptossh.ServerConn, cryptossh.NewChannel)) string {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := cryptossh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	config := &cryptossh.ServerConfig{
		PasswordCallback: func(_ cryptossh.ConnMetadata, supplied []byte) (*cryptossh.Permissions, error) {
			if string(supplied) != password {
				return nil, errors.New("password rejected")
			}
			return nil, nil
		},
	}
	config.AddHostKey(signer)
	return serveTCP(t, func(raw net.Conn) {
		conn, channels, requests, err := cryptossh.NewServerConn(raw, config)
		if err != nil {
			return
		}
		defer conn.Close()
		go cryptossh.DiscardRequests(requests)
		for channel := range channels {
			handle(conn, channel)
		}
	})
}

func connectSSH(t *testing.T, address string) *Client {
	t.Helper()
	client, err := newClientAt(context.Background(), Config{Username: "root", Password: "proto", Timeout: time.Second}, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func acceptExec(t *testing.T, incoming cryptossh.NewChannel, handle func(cryptossh.Channel, *cryptossh.Request, string)) {
	t.Helper()
	channel, requests, err := incoming.Accept()
	if err != nil {
		return
	}
	defer channel.Close()
	for request := range requests {
		if request.Type != "exec" {
			request.Reply(false, nil)
			continue
		}
		var payload struct{ Command string }
		if err := cryptossh.Unmarshal(request.Payload, &payload); err != nil {
			t.Errorf("decode exec: %v", err)
			return
		}
		handle(channel, request, payload.Command)
		return
	}
}

func exitStatus(channel cryptossh.Channel, status uint32) {
	channel.SendRequest("exit-status", false, cryptossh.Marshal(struct{ Status uint32 }{status}))
}

func receiveError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("operation did not finish promptly")
		return nil
	}
}

func awaitReady(t *testing.T, ready <-chan struct{}) {
	t.Helper()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("server did not reach requested phase")
	}
}

func TestSSHHandshakeErrors(t *testing.T) {
	t.Run("authentication", func(t *testing.T) {
		address := serveSSH(t, "different", func(_ *cryptossh.ServerConn, channel cryptossh.NewChannel) {
			channel.Reject(cryptossh.Prohibited, "not expected")
		})
		_, err := newClientAt(context.Background(), Config{Username: "root", Password: "proto", Timeout: time.Second}, address)
		if !errors.Is(err, ErrAuthFailed) {
			t.Fatalf("error = %v, want ErrAuthFailed", err)
		}
		// The authentication sentinel must not replace the SSH cause.
		if !strings.Contains(err.Error(), "unable to authenticate") {
			t.Fatalf("authentication cause lost: %v", err)
		}
	})
	for _, mode := range []string{"EOF", "negotiation", "configured timeout", "parent deadline", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ready := make(chan struct{})
			address := serveTCP(t, func(conn net.Conn) {
				close(ready)
				switch mode {
				case "EOF":
					return
				case "negotiation":
					io.WriteString(conn, "SSH-1.5-unsupported\r\n")
					return
				default:
					io.Copy(io.Discard, conn)
				}
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			config := Config{Username: "root", Password: "proto", Timeout: time.Second}
			var want error
			if mode == "configured timeout" {
				config.Timeout = 50 * time.Millisecond
				want = context.DeadlineExceeded
			}
			if mode == "parent deadline" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 50*time.Millisecond)
				defer stop()
				want = context.DeadlineExceeded
			}
			result := make(chan error, 1)
			go func() {
				_, err := newClientAt(ctx, config, address)
				result <- err
			}()
			awaitReady(t, ready)
			if mode == "cancel" {
				cancel()
				want = context.Canceled
			}
			err := receiveError(t, result)
			if err == nil || errors.Is(err, ErrAuthFailed) || (want != nil && !errors.Is(err, want)) {
				t.Fatalf("error = %v, want non-authentication failure %v", err, want)
			}
		})
	}
}

func TestSSHCommandFailures(t *testing.T) {
	for _, operation := range []string{"Run", "Reboot", "EnterFastboot"} {
		for _, outcome := range []string{"okay", "open rejected", "exec rejected", "disconnect before exec", "nonzero", "signal", "missing status"} {
			t.Run(operation+"/"+outcome, func(t *testing.T) {
				address := serveSSH(t, "proto", func(conn *cryptossh.ServerConn, incoming cryptossh.NewChannel) {
					if outcome == "open rejected" {
						incoming.Reject(cryptossh.Prohibited, "session rejected")
						return
					}
					acceptExec(t, incoming, func(channel cryptossh.Channel, request *cryptossh.Request, command string) {
						switch outcome {
						case "exec rejected":
							request.Reply(false, nil)
							return
						case "disconnect before exec":
							conn.Close()
							return
						}
						request.Reply(true, nil)
						switch outcome {
						case "okay":
							io.WriteString(channel, "stdout\n")
							io.WriteString(channel.Stderr(), "stderr\n")
							exitStatus(channel, 0)
						case "nonzero":
							exitStatus(channel, 7)
						case "signal":
							channel.SendRequest("exit-signal", false, cryptossh.Marshal(struct {
								Signal     string
								CoreDumped bool
								Message    string
								Language   string
							}{"TERM", false, "terminated", "en"}))
						case "missing status":
							conn.Close()
						}
					})
				})
				client := connectSSH(t, address)
				var err error
				switch operation {
				case "Run":
					var output string
					output, err = client.Run(context.Background(), "example")
					if outcome == "okay" && (!strings.Contains(output, "stdout") || !strings.Contains(output, "stderr")) {
						t.Fatalf("combined output = %q", output)
					}
				case "Reboot":
					err = client.Reboot(context.Background())
				case "EnterFastboot":
					err = client.EnterFastbootWithNetwork(context.Background(), "", "", "")
				}
				wantSuccess := outcome == "okay" || (outcome == "missing status" && operation != "Run")
				if (err == nil) != wantSuccess {
					t.Fatalf("error = %v, want success %v", err, wantSuccess)
				}
				if outcome == "nonzero" || outcome == "signal" {
					var exit *cryptossh.ExitError
					if !errors.As(err, &exit) {
						t.Fatalf("exit cause lost: %v", err)
					}
					if outcome == "nonzero" && exit.ExitStatus() != 7 {
						t.Errorf("status = %d, want 7", exit.ExitStatus())
					}
					if outcome == "signal" && exit.Signal() != "TERM" {
						t.Errorf("signal = %q, want TERM", exit.Signal())
					}
				}
			})
		}
	}
}

func TestSSHSudoFailureDiagnostics(t *testing.T) {
	for _, operation := range []string{"Run", "Reboot", "EnterFastboot"} {
		t.Run(operation, func(t *testing.T) {
			const diagnostic = "sudo: a password is required"
			address := serveSSH(t, "proto", func(_ *cryptossh.ServerConn, incoming cryptossh.NewChannel) {
				acceptExec(t, incoming, func(channel cryptossh.Channel, request *cryptossh.Request, command string) {
					request.Reply(true, nil)
					// Execute the received shell syntax without invoking real sudo or reboot.
					cmd := exec.Command("sh", "-c", "sudo() { printf '%s\n' '"+diagnostic+"' >&2; return 7; }; "+command)
					cmd.Stdout, cmd.Stderr = channel, channel.Stderr()
					status := uint32(0)
					if err := cmd.Run(); err != nil {
						var exit *exec.ExitError
						if !errors.As(err, &exit) {
							t.Errorf("running shell: %v", err)
							return
						}
						status = uint32(exit.ExitCode())
					}
					exitStatus(channel, status)
				})
			})
			client, err := newClientAt(context.Background(), Config{Username: "admin", Password: "proto", Timeout: time.Second}, address)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			switch operation {
			case "Run":
				_, err = client.Run(context.Background(), "sudo -n reboot")
			case "Reboot":
				err = client.Reboot(context.Background())
			case "EnterFastboot":
				err = client.EnterFastbootWithNetwork(context.Background(), "", "", "")
			}
			var exit *cryptossh.ExitError
			if !errors.As(err, &exit) || exit.ExitStatus() != 7 || !strings.Contains(err.Error(), diagnostic) {
				t.Fatalf("error = %v, want sudo diagnostic and SSH exit status 7", err)
			}
		})
	}
}

func TestSSHCommandCancellation(t *testing.T) {
	for _, operation := range []string{"Run", "Reboot", "EnterFastboot"} {
		for _, phase := range []string{"open", "exec", "wait"} {
			t.Run(operation+"/"+phase, func(t *testing.T) {
				ready := make(chan struct{})
				var commands atomic.Int32
				address := serveSSH(t, "proto", func(conn *cryptossh.ServerConn, incoming cryptossh.NewChannel) {
					if phase == "open" {
						close(ready)
						conn.Wait()
						return
					}
					acceptExec(t, incoming, func(_ cryptossh.Channel, request *cryptossh.Request, _ string) {
						commands.Add(1)
						if phase == "wait" {
							request.Reply(true, nil)
						}
						close(ready)
						conn.Wait()
					})
				})
				client := connectSSH(t, address)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				result := make(chan error, 1)
				go func() {
					var err error
					switch operation {
					case "Run":
						_, err = client.Run(ctx, "example")
					case "Reboot":
						err = client.Reboot(ctx)
					case "EnterFastboot":
						err = client.EnterFastbootWithNetwork(ctx, "", "", "")
					}
					result <- err
				}()
				awaitReady(t, ready)
				cancel()
				if err := receiveError(t, result); !errors.Is(err, context.Canceled) {
					t.Fatalf("error = %v, want context.Canceled", err)
				}
				if _, err := client.Run(context.Background(), "must not reuse canceled client"); err == nil {
					t.Error("canceled connection reused")
				}
				if commands.Load() > 1 {
					t.Errorf("received %d commands after cancellation", commands.Load())
				}
			})
		}
	}
}

func TestFastbootNetworkValidation(t *testing.T) {
	var commands atomic.Int32
	address := serveSSH(t, "proto", func(_ *cryptossh.ServerConn, incoming cryptossh.NewChannel) {
		commands.Add(1)
		incoming.Reject(cryptossh.Prohibited, "no commands expected")
	})
	client := connectSSH(t, address)
	invalid := []string{" 192.0.2.1", "192.0.2.1 ", "192.0.2.1\n", "192.0.2.1;reboot", "$(reboot)", "`reboot`", "'", "192.0.2.1/24", "192.0.2.1:22", "::1", "::ffff:192.0.2.1", "miner.local", "192.0.2", "192.000.2.1", "256.0.0.1", "1", "\t"}
	for field := range 3 {
		for _, value := range invalid {
			t.Run(fmt.Sprintf("field%d/%q", field, value), func(t *testing.T) {
				args := [3]string{}
				args[field] = value
				if err := ValidateFastbootNetwork(args[0], args[1], args[2]); err == nil {
					t.Error("invalid network accepted")
				}
				if err := client.EnterFastbootWithNetwork(context.Background(), args[0], args[1], args[2]); err == nil {
					t.Error("invalid network entered fastboot")
				}
			})
		}
	}
	for _, mask := range []string{"255.0.255.0", "255.255.255.1", "127.0.0.0"} {
		if err := client.EnterFastbootWithNetwork(context.Background(), "", mask, ""); err == nil {
			t.Errorf("noncontiguous mask %q accepted", mask)
		}
	}
	if commands.Load() != 0 {
		t.Fatalf("invalid settings sent %d remote commands", commands.Load())
	}
}

// Only valid inputs reach this shell. Functions intercept every privileged command;
// positional arguments are decoded by a real shell without rebooting the host.
const fakeRebootShell = `reboot() { printf '%s\000' reboot "$@"; }
systemctl() { printf '%s\000' unexpected-service-command; return 17; }
sudo() { [ "$1" = -n ] && shift; "$@"; }
`

func TestFastbootNetworkPositionals(t *testing.T) {
	for _, username := range []string{"root", "admin"} {
		for _, args := range [][3]string{{}, {"192.0.2.2", "", ""}, {"", "255.255.255.0", ""}, {"", "", "192.0.2.1"}, {"192.0.2.2", "255.255.255.0", "192.0.2.1"}, {"0.0.0.0", "0.0.0.0", "255.255.255.255"}, {"", "255.255.255.255", ""}} {
			t.Run(username+fmt.Sprint(args), func(t *testing.T) {
				var commands atomic.Int32
				outputs := make(chan string, 2)
				address := serveSSH(t, "proto", func(_ *cryptossh.ServerConn, incoming cryptossh.NewChannel) {
					acceptExec(t, incoming, func(channel cryptossh.Channel, request *cryptossh.Request, command string) {
						commands.Add(1)
						request.Reply(true, nil)
						output, err := exec.Command("sh", "-c", fakeRebootShell+command).CombinedOutput()
						outputs <- string(output)
						if err != nil {
							t.Errorf("shell command failed: %v: %s", err, output)
							exitStatus(channel, 1)
							return
						}
						exitStatus(channel, 0)
					})
				})
				client, err := newClientAt(context.Background(), Config{Username: username, Password: "proto", Timeout: time.Second}, address)
				if err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				if err := client.EnterFastbootWithNetwork(context.Background(), args[0], args[1], args[2]); err != nil {
					t.Fatal(err)
				}
				if commands.Load() != 1 {
					t.Fatalf("received %d commands, want reboot only", commands.Load())
				}
				got := strings.Split(strings.TrimSuffix(<-outputs, "\x00"), "\x00")
				want := []string{"reboot", "--fastboot"}
				if args != [3]string{} {
					want = append(want, args[:]...)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("shell arguments = %q, want %q", got, want)
				}
			})
		}
	}
}

func TestSSHCommandTimeouts(t *testing.T) {
	for _, operation := range []string{"Run", "Reboot", "EnterFastboot", "short private bound"} {
		t.Run(operation, func(t *testing.T) {
			address := serveSSH(t, "proto", func(conn *cryptossh.ServerConn, incoming cryptossh.NewChannel) {
				acceptExec(t, incoming, func(_ cryptossh.Channel, request *cryptossh.Request, _ string) {
					request.Reply(true, nil)
					conn.Wait()
				})
			})
			client := connectSSH(t, address)
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				var err error
				switch operation {
				case "Run":
					_, err = client.Run(ctx, "example")
				case "Reboot":
					err = client.Reboot(ctx)
				case "EnterFastboot":
					err = client.EnterFastbootWithNetwork(ctx, "", "", "")
				default:
					_, err = client.run(context.Background(), "example", 50*time.Millisecond)
				}
				result <- err
			}()
			if err := receiveError(t, result); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error = %v, want context.DeadlineExceeded", err)
			}
		})
	}
}

func TestSSHDefaultCommandDeadline(t *testing.T) {
	for _, operation := range []string{"Run", "Reboot"} {
		t.Run(operation, func(t *testing.T) {
			address := serveSSH(t, "proto", func(conn *cryptossh.ServerConn, incoming cryptossh.NewChannel) {
				acceptExec(t, incoming, func(_ cryptossh.Channel, request *cryptossh.Request, _ string) {
					request.Reply(true, nil)
					conn.Wait()
				})
			})
			client := connectSSH(t, address)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			start := time.Now()
			var err error
			if operation == "Run" {
				_, err = client.Run(ctx, "example")
			} else {
				err = client.Reboot(ctx)
			}
			if !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
				t.Fatalf("error = %v, parent = %v; want command deadline", err, ctx.Err())
			}
			if elapsed := time.Since(start); elapsed < 9*time.Second || elapsed > 15*time.Second {
				t.Fatalf("ordinary command took %v, want 10-second limit", elapsed)
			}
		})
	}
}

func TestSSHCanceledBeforeCommand(t *testing.T) {
	for _, operation := range []string{"Run", "Reboot", "EnterFastboot"} {
		t.Run(operation, func(t *testing.T) {
			var opened atomic.Int32
			address := serveSSH(t, "proto", func(_ *cryptossh.ServerConn, incoming cryptossh.NewChannel) {
				opened.Add(1)
				incoming.Reject(cryptossh.Prohibited, "unexpected session")
			})
			client := connectSSH(t, address)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			var err error
			switch operation {
			case "Run":
				_, err = client.Run(ctx, "example")
			case "Reboot":
				err = client.Reboot(ctx)
			case "EnterFastboot":
				err = client.EnterFastbootWithNetwork(ctx, "", "", "")
			}
			if !errors.Is(err, context.Canceled) || opened.Load() != 0 {
				t.Fatalf("error = %v, sessions = %d; want cancellation without session", err, opened.Load())
			}
		})
	}
}
