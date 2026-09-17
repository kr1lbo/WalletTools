package worker

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	provanitycrypto "github.com/woomai/provanity/internal/crypto"
	"github.com/woomai/provanity/internal/gpu"
	"github.com/woomai/provanity/internal/local"
	"github.com/woomai/provanity/internal/vanity"
)

func TestWorkerLocalModeResultIncludesPrivateKey(t *testing.T) {
	privateKey := fixedHex(t, "0000000000000000000000000000000000000000000000000000000000000001")
	address, err := provanitycrypto.AddressFromPrivateKey(privateKey)
	if err != nil {
		t.Fatalf("AddressFromPrivateKey: %v", err)
	}

	stdout, stderr, err := executeCommand(t, []string{"--pattern", "pattern:XX"}, "", func(ctx context.Context, opts local.Options, emit local.EmitFunc) (local.Result, error) {
		if opts.Wallet != local.WalletEVM {
			t.Fatalf("Wallet = %q, want %q", opts.Wallet, local.WalletEVM)
		}
		if len(opts.InitialPublicKey) != 0 {
			t.Fatalf("InitialPublicKey = %x, want empty", opts.InitialPublicKey)
		}
		return local.Result{
			Address:    "0x" + hex.EncodeToString(address),
			PrivateKey: "0x" + hex.EncodeToString(privateKey),
			Offset:     "0x" + strings.Repeat("0", 64),
			Stats:      local.Stats{ElapsedSec: 1, Attempts: 1},
		}, nil
	})
	if err != nil {
		t.Fatalf("worker returned error: %v\nstderr:\n%s", err, stderr)
	}

	result := lastEvent(t, stdout, "result")
	gotPrivateKey, ok := result["private_key"].(string)
	if !ok || gotPrivateKey == "" {
		t.Fatalf("private_key missing in result event: %#v", result)
	}
	gotAddress, ok := result["address"].(string)
	if !ok {
		t.Fatalf("address missing in result event: %#v", result)
	}
	decodedPrivateKey := fixedHex(t, strings.TrimPrefix(gotPrivateKey, "0x"))
	derivedAddress, err := provanitycrypto.AddressFromPrivateKey(decodedPrivateKey)
	if err != nil {
		t.Fatalf("AddressFromPrivateKey(result.private_key): %v", err)
	}
	if gotAddress != "0x"+hex.EncodeToString(derivedAddress) {
		t.Fatalf("address = %s, want derived 0x%x", gotAddress, derivedAddress)
	}
}

func TestWorkerTronModeUsesTronWalletAndPattern(t *testing.T) {
	stdout, stderr, err := executeCommand(t, []string{"--mode", "tron", "--pattern", "prefix:ABC"}, "", func(ctx context.Context, opts local.Options, emit local.EmitFunc) (local.Result, error) {
		if opts.Wallet != local.WalletTron {
			t.Fatalf("Wallet = %q, want %q", opts.Wallet, local.WalletTron)
		}
		if opts.Pattern.Kind != vanity.PatternTronPrefix {
			t.Fatalf("Pattern.Kind = %q, want %q", opts.Pattern.Kind, vanity.PatternTronPrefix)
		}
		emit(local.RunEvent{Candidate: &local.Candidate{
			Address:     "TABC111111111111111111111111111111",
			Offset:      strings.Repeat("0", 64),
			Score:       3,
			TargetScore: 3,
			Matched:     true,
		}})
		return local.Result{
			Address:     "TABC111111111111111111111111111111",
			Offset:      "0x" + strings.Repeat("0", 64),
			Score:       3,
			TargetScore: 3,
			Stats:       local.Stats{ElapsedSec: 1, Attempts: 1},
		}, nil
	})
	if err != nil {
		t.Fatalf("worker returned error: %v\nstderr:\n%s", err, stderr)
	}

	candidate := lastEvent(t, stdout, "candidate")
	if candidate["address"] != "TABC111111111111111111111111111111" {
		t.Fatalf("candidate address = %v, want Tron address without 0x", candidate["address"])
	}
	result := lastEvent(t, stdout, "result")
	if result["address"] != "TABC111111111111111111111111111111" {
		t.Fatalf("result address = %v, want Tron address", result["address"])
	}
}

func TestWorkerRejectsInvalidMode(t *testing.T) {
	called := false
	_, stderr, err := executeCommand(t, []string{
		"--mode", "solana",
		"--pattern", "pattern:XX",
	}, "", func(ctx context.Context, opts local.Options, emit local.EmitFunc) (local.Result, error) {
		called = true
		return local.Result{}, nil
	})
	if err == nil {
		t.Fatal("invalid mode unexpectedly succeeded")
	}
	if called {
		t.Fatal("worker run started after invalid mode")
	}
	if !strings.Contains(stderr, "--mode must be evm or tron") {
		t.Fatalf("stderr = %q, want invalid mode error", stderr)
	}
}

func TestWorkerRemoteModeResultOmitsPrivateKey(t *testing.T) {
	initPrivateKey := fixedHex(t, "0000000000000000000000000000000000000000000000000000000000000001")
	initPublicKey, err := provanitycrypto.PublicKeyFromPrivateKey(initPrivateKey)
	if err != nil {
		t.Fatalf("PublicKeyFromPrivateKey: %v", err)
	}
	offset := fixedHex(t, "0000000000000000000000000000000000000000000000000000000000000001")
	finalPrivateKey, err := provanitycrypto.ComposePrivateKey(initPrivateKey, offset)
	if err != nil {
		t.Fatalf("ComposePrivateKey: %v", err)
	}
	address, err := provanitycrypto.AddressFromPrivateKey(finalPrivateKey)
	if err != nil {
		t.Fatalf("AddressFromPrivateKey: %v", err)
	}

	stdout, stderr, err := executeCommand(t, []string{
		"--pattern", "pattern:XX",
		"--init-pub", hex.EncodeToString(initPublicKey),
	}, "", func(ctx context.Context, opts local.Options, emit local.EmitFunc) (local.Result, error) {
		if !bytes.Equal(opts.InitialPublicKey, initPublicKey) {
			t.Fatalf("InitialPublicKey = %x, want %x", opts.InitialPublicKey, initPublicKey)
		}
		return local.Result{
			Address: "0x" + hex.EncodeToString(address),
			Offset:  "0x" + hex.EncodeToString(offset),
			Stats:   local.Stats{ElapsedSec: 1, Attempts: 1},
		}, nil
	})
	if err != nil {
		t.Fatalf("worker returned error: %v\nstderr:\n%s", err, stderr)
	}

	result := lastEvent(t, stdout, "result")
	if _, ok := result["private_key"]; ok {
		t.Fatalf("private_key present in remote result: %#v", result)
	}
	gotOffset := strings.TrimPrefix(result["offset"].(string), "0x")
	composed, err := provanitycrypto.ComposePrivateKey(initPrivateKey, fixedHex(t, gotOffset))
	if err != nil {
		t.Fatalf("ComposePrivateKey(result.offset): %v", err)
	}
	composedAddress, err := provanitycrypto.AddressFromPrivateKey(composed)
	if err != nil {
		t.Fatalf("AddressFromPrivateKey(composed): %v", err)
	}
	if result["address"] != "0x"+hex.EncodeToString(composedAddress) {
		t.Fatalf("address = %v, want 0x%x", result["address"], composedAddress)
	}
}

func TestWorkerProgressIncludesDeviceHashrates(t *testing.T) {
	stdout, stderr, err := executeCommand(t, []string{
		"--pattern", "pattern:XX",
		"--progress-interval", "1",
	}, "", func(ctx context.Context, opts local.Options, emit local.EmitFunc) (local.Result, error) {
		emit(local.RunEvent{GPUEvent: gpu.ProgressEvent{
			Type:              gpu.EventProgress,
			ElapsedSec:        1,
			ElapsedMS:         1000,
			Attempts:          3000,
			Hashrate:          3000,
			HashrateUncertain: true,
			Devices: []gpu.Device{
				{ID: 0, Name: "Test GPU 0", GlobalMem: 12 * 1024 * 1024 * 1024, Hashrate: 1000},
				{ID: 1, Name: "Test GPU 1", GlobalMem: 24 * 1024 * 1024 * 1024, Hashrate: 2000},
			},
		}})
		return local.Result{
			Address: "0x" + strings.Repeat("0", 40),
			Offset:  "0x" + strings.Repeat("0", 64),
			Stats:   local.Stats{ElapsedSec: 1, Attempts: 3000, Hashrate: 3000},
		}, nil
	})
	if err != nil {
		t.Fatalf("worker returned error: %v\nstderr:\n%s", err, stderr)
	}

	progress := lastEvent(t, stdout, "progress")
	devices, ok := progress["devices"].([]any)
	if !ok || len(devices) != 2 {
		t.Fatalf("progress devices = %#v, want two devices", progress["devices"])
	}
	first, ok := devices[0].(map[string]any)
	if !ok {
		t.Fatalf("first progress device = %#v", devices[0])
	}
	if first["memory_mib"] != float64(12288) || first["hashrate"] != float64(1000) {
		t.Fatalf("first progress device = %#v, want memory_mib and hashrate", first)
	}
}

func TestWorkerEmitsPhaseEvents(t *testing.T) {
	stdout, stderr, err := executeCommand(t, []string{
		"--pattern", "pattern:XX",
	}, "", func(ctx context.Context, opts local.Options, emit local.EmitFunc) (local.Result, error) {
		emit(local.RunEvent{GPUEvent: gpu.PhaseEvent{
			Type:     gpu.EventPhase,
			DeviceID: 0,
			Phase:    "init_lanes",
			Message:  "initializing 1,020,000 lanes",
			Value:    1020000,
		}})
		return local.Result{
			Address: "0x" + strings.Repeat("0", 40),
			Offset:  "0x" + strings.Repeat("0", 64),
			Stats:   local.Stats{ElapsedSec: 1, Attempts: 1},
		}, nil
	})
	if err != nil {
		t.Fatalf("worker returned error: %v\nstderr:\n%s", err, stderr)
	}

	phase := lastEvent(t, stdout, "phase")
	if phase["phase"] != "init_lanes" {
		t.Fatalf("phase = %v, want init_lanes", phase["phase"])
	}
	if phase["message"] != "initializing 1,020,000 lanes" {
		t.Fatalf("message = %v, want human-readable init message", phase["message"])
	}
	if phase["device_id"] != float64(0) {
		t.Fatalf("device_id = %v, want 0", phase["device_id"])
	}
	if phase["value"] != float64(1020000) {
		t.Fatalf("value = %v, want 1020000", phase["value"])
	}
}

func TestWorkerStdinHandshake(t *testing.T) {
	initPrivateKey := fixedHex(t, "0000000000000000000000000000000000000000000000000000000000000001")
	initPublicKey, err := provanitycrypto.PublicKeyFromPrivateKey(initPrivateKey)
	if err != nil {
		t.Fatalf("PublicKeyFromPrivateKey: %v", err)
	}

	stdout, stderr, err := executeCommand(t, []string{
		"--pattern", "pattern:XX",
		"--init-pub-stdin",
	}, fmt.Sprintf(`{"init_pub":"%s"}`+"\n", hex.EncodeToString(initPublicKey)), func(ctx context.Context, opts local.Options, emit local.EmitFunc) (local.Result, error) {
		if !bytes.Equal(opts.InitialPublicKey, initPublicKey) {
			t.Fatalf("InitialPublicKey = %x, want %x", opts.InitialPublicKey, initPublicKey)
		}
		return local.Result{
			Address: "0x" + strings.Repeat("0", 40),
			Offset:  "0x" + strings.Repeat("0", 64),
			Stats:   local.Stats{ElapsedSec: 1, Attempts: 1},
		}, nil
	})
	if err != nil {
		t.Fatalf("worker returned error: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	called := false
	stdout, stderr, err = executeCommand(t, []string{
		"--pattern", "pattern:XX",
		"--init-pub-stdin",
	}, "{bad}\n", func(ctx context.Context, opts local.Options, emit local.EmitFunc) (local.Result, error) {
		called = true
		return local.Result{}, nil
	})
	if err == nil {
		t.Fatal("malformed stdin JSON unexpectedly succeeded")
	}
	if called {
		t.Fatal("worker run started after malformed stdin JSON")
	}
	if !strings.Contains(stderr, "parse stdin handshake JSON") {
		t.Fatalf("stderr = %q, want parse error", stderr)
	}
	if lastEvent(t, stdout, "error")["event"] != "error" {
		t.Fatalf("stdout did not contain error event:\n%s", stdout)
	}

	called = false
	_, stderr, err = executeCommand(t, []string{
		"--pattern", "pattern:XX",
		"--init-pub-stdin",
	}, `{"init_pub":"abcd"}`+"\n", func(ctx context.Context, opts local.Options, emit local.EmitFunc) (local.Result, error) {
		called = true
		return local.Result{}, nil
	})
	if err == nil {
		t.Fatal("short init_pub unexpectedly succeeded")
	}
	if called {
		t.Fatal("worker run started after invalid init_pub")
	}
	if !strings.Contains(stderr, "init_pub must be 128 hex chars") {
		t.Fatalf("stderr = %q, want init_pub length error", stderr)
	}
}

func TestWorkerConflictFlags(t *testing.T) {
	called := false
	_, stderr, err := executeCommand(t, []string{
		"--pattern", "pattern:XX",
		"--init-pub", "abcd",
		"--init-pub-stdin",
	}, `{"init_pub":"abcd"}`+"\n", func(ctx context.Context, opts local.Options, emit local.EmitFunc) (local.Result, error) {
		called = true
		return local.Result{}, nil
	})
	if err == nil {
		t.Fatal("conflicting init pub flags unexpectedly succeeded")
	}
	if called {
		t.Fatal("worker run started after conflicting init pub flags")
	}
	if !strings.Contains(stderr, "mutually exclusive") {
		t.Fatalf("stderr = %q, want conflict error", stderr)
	}

	called = false
	_, stderr, err = executeCommand(t, []string{
		"--pattern", "pattern:XX",
		"--ws-server-port", "1",
		"--ws-key", strings.Repeat("0", 64),
		"--ws-key-stdin",
	}, `{"ws_key":"`+strings.Repeat("1", 64)+`"}`+"\n", func(ctx context.Context, opts local.Options, emit local.EmitFunc) (local.Result, error) {
		called = true
		return local.Result{}, nil
	})
	if err == nil {
		t.Fatal("conflicting ws key flags unexpectedly succeeded")
	}
	if called {
		t.Fatal("worker run started after conflicting ws key flags")
	}
	if !strings.Contains(stderr, "mutually exclusive") {
		t.Fatalf("stderr = %q, want conflict error", stderr)
	}
}

func TestWorkerWSServerRequiresKey(t *testing.T) {
	called := false
	_, stderr, err := executeCommand(t, []string{
		"--pattern", "pattern:XX",
		"--ws-server-port", "1",
	}, "", func(ctx context.Context, opts local.Options, emit local.EmitFunc) (local.Result, error) {
		called = true
		return local.Result{}, nil
	})
	if err == nil {
		t.Fatal("missing ws key unexpectedly succeeded")
	}
	if called {
		t.Fatal("worker run started without ws key")
	}
	if !strings.Contains(stderr, "--ws-server-port requires --ws-key or --ws-key-stdin") {
		t.Fatalf("stderr = %q, want missing ws key error", stderr)
	}
}

func TestWorkerEncryptedWSPingStopAndBadKey(t *testing.T) {
	key := fixedHex(t, strings.Repeat("a", 64))
	badKey := fixedHex(t, strings.Repeat("b", 64))
	port := freePort(t)
	started := make(chan struct{})
	result := local.Result{
		Address: "0x" + strings.Repeat("2", 40),
		Offset:  "0x" + strings.Repeat("0", 64),
		Partial: true,
		Stats:   local.Stats{ElapsedSec: 1, Attempts: 1},
	}
	running := startCommand(t, []string{
		"--pattern", "pattern:XX",
		"--ws-server-port", fmt.Sprint(port),
		"--ws-key", hex.EncodeToString(key),
	}, "", func(ctx context.Context, opts local.Options, emit local.EmitFunc) (local.Result, error) {
		close(started)
		emit(local.RunEvent{GPUEvent: gpu.ReadyEvent{Type: gpu.EventReady, Devices: []gpu.Device{{ID: 0, Name: "Test GPU"}}}})
		<-ctx.Done()
		return result, nil
	})
	defer running.restore()
	waitForClosed(t, started, "worker start")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://127.0.0.1:%d/", port), nil)
	if err != nil {
		t.Fatalf("Dial encrypted WS: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	event := readWSJSON(t, ctx, conn, key)
	if event["event"] != "subscribed" {
		t.Fatalf("first WS event = %#v, want subscribed", event)
	}
	writeWSJSON(t, ctx, conn, key, map[string]any{"command": "ping"})
	event = readWSJSON(t, ctx, conn, key)
	if event["event"] != "pong" {
		t.Fatalf("ping response = %#v, want pong", event)
	}

	badConn, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://127.0.0.1:%d/", port), nil)
	if err != nil {
		t.Fatalf("Dial bad-key WS: %v", err)
	}
	_ = readWSJSON(t, ctx, badConn, key)
	writeWSJSON(t, ctx, badConn, badKey, map[string]any{"command": "ping"})
	if _, _, err := badConn.Read(ctx); err == nil {
		t.Fatal("bad-key connection stayed open after decrypt failure")
	}
	select {
	case err := <-running.done:
		t.Fatalf("worker exited after bad-key frame: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	writeWSJSON(t, ctx, conn, key, map[string]any{"command": "stop"})
	event = readWSJSON(t, ctx, conn, key)
	if event["event"] != "result" {
		t.Fatalf("stop response = %#v, want result", event)
	}
	if err := running.wait(); err != nil {
		t.Fatalf("worker returned error: %v\nstderr:\n%s", err, running.stderr.String())
	}
	final := lastEvent(t, running.stdout.String(), "result")
	if final["partial"] != true {
		t.Fatalf("final result = %#v, want partial true", final)
	}
}

func executeCommand(t *testing.T, args []string, stdin string, run runFunc) (string, string, error) {
	t.Helper()
	running := startCommand(t, args, stdin, run)
	defer running.restore()
	err := running.wait()
	return running.stdout.String(), running.stderr.String(), err
}

type runningCommand struct {
	stdout  bytes.Buffer
	stderr  bytes.Buffer
	done    chan error
	restore func()
}

func startCommand(t *testing.T, args []string, stdin string, run runFunc) *runningCommand {
	t.Helper()
	oldRun := runSearch
	runSearch = run

	cmd := newCommand()
	cmd.SetArgs(args)
	running := &runningCommand{
		done: make(chan error, 1),
		restore: func() {
			runSearch = oldRun
		},
	}
	cmd.SetOut(&running.stdout)
	cmd.SetErr(&running.stderr)
	if stdin != "" {
		cmd.SetIn(strings.NewReader(stdin))
	}
	go func() {
		running.done <- cmd.Execute()
	}()
	return running
}

func (r *runningCommand) wait() error {
	select {
	case err := <-r.done:
		return err
	case <-time.After(5 * time.Second):
		return fmt.Errorf("worker command timed out")
	}
}

func parseEvents(t *testing.T, stdout string) []map[string]any {
	t.Helper()
	var events []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode worker event %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func lastEvent(t *testing.T, stdout, eventName string) map[string]any {
	t.Helper()
	events := parseEvents(t, stdout)
	for i := len(events) - 1; i >= 0; i-- {
		if events[i]["event"] == eventName {
			return events[i]
		}
	}
	t.Fatalf("no %q event in stdout:\n%s", eventName, stdout)
	return nil
}

func fixedHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatalf("decode hex %q: %v", value, err)
	}
	return decoded
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func waitForClosed(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func readWSJSON(t *testing.T, ctx context.Context, conn *websocket.Conn, key []byte) map[string]any {
	t.Helper()
	messageType, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read WS message: %v", err)
	}
	plain, err := decodeWSFrame(key, messageType, data)
	if err != nil {
		t.Fatalf("decode WS frame: %v", err)
	}
	var event map[string]any
	if err := json.Unmarshal(plain, &event); err != nil {
		t.Fatalf("decode WS JSON %q: %v", string(plain), err)
	}
	return event
}

func writeWSJSON(t *testing.T, ctx context.Context, conn *websocket.Conn, key []byte, value map[string]any) {
	t.Helper()
	messageType, data, err := encodeWSFrame(key, value)
	if err != nil {
		t.Fatalf("encode WS frame: %v", err)
	}
	if err := conn.Write(ctx, messageType, data); err != nil {
		t.Fatalf("write WS message: %v", err)
	}
}
