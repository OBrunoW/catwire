package session

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/OBrunoW/catwire/internal/domain"
)

func TestConcurrentConnectUnpairedKeepsValidQR(t *testing.T) {
	instance := unpairedInstance()
	h := newHarness(t, instance)
	h.factory.prepare = func(client *fakeClient) {
		client.qrItems <- QRItem{Kind: QRItemCode, Code: "first-code", Timeout: time.Minute}
	}
	results := make([]ConnectResult, 2)
	errs := make([]error, 2)
	var group sync.WaitGroup
	for index := range results {
		group.Add(1)
		go func() {
			defer group.Done()
			results[index], errs[index] = h.manager.Connect(context.Background(), instance.ID)
		}()
	}
	group.Wait()
	for index := range results {
		if errs[index] != nil {
			t.Fatalf("connect %d: %v", index, errs[index])
		}
		if results[index].QR == nil || results[index].QR.Code != "first-code" {
			t.Fatalf("result %d = %+v", index, results[index])
		}
	}
	if h.factory.count() != 1 || h.factory.last().called("disconnect") {
		t.Fatalf("clients = %d calls = %v", h.factory.count(), h.factory.last().calls)
	}
}

func TestReconnectRechecksAutoConnectUnderLock(t *testing.T) {
	instance := pairedInstance(domain.InstanceStatusDisconnected)
	h := newHarness(t, instance)
	current, err := h.manager.entryFor(instance.ID)
	if err != nil {
		t.Fatalf("entry: %v", err)
	}
	current.mu.Lock()
	done := make(chan error, 1)
	go func() {
		done <- h.manager.reconnect(context.Background(), instance.ID, shouldReconnectOnWatchdog)
	}()
	time.Sleep(20 * time.Millisecond)
	h.store.setAutoConnect(instance.ID, false)
	current.mu.Unlock()
	if err := <-done; err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if h.factory.count() != 0 {
		t.Fatal("reconnect must not revive an instance with auto_connect disabled")
	}
	if h.store.snapshot(instance.ID).Status != domain.InstanceStatusDisconnected {
		t.Fatalf("status = %s", h.store.snapshot(instance.ID).Status)
	}
}

func TestDisconnectCancelsQRContext(t *testing.T) {
	instance := unpairedInstance()
	h := newHarness(t, instance)
	h.factory.prepare = func(client *fakeClient) {
		client.qrItems <- QRItem{Kind: QRItemCode, Code: "code", Timeout: time.Minute}
	}
	if _, err := h.manager.Connect(context.Background(), instance.ID); err != nil {
		t.Fatalf("connect: %v", err)
	}
	client := h.factory.last()
	qrContext := client.qrContext()
	if qrContext.Err() != nil {
		t.Fatal("qr context must be alive while connecting")
	}
	if _, err := h.manager.Disconnect(context.Background(), instance.ID); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	select {
	case <-qrContext.Done():
	case <-time.After(time.Second):
		t.Fatal("qr context was not cancelled after disconnect")
	}
}

func TestStaleGenerationQRItemIsDiscarded(t *testing.T) {
	instance := unpairedInstance()
	h := newHarness(t, instance)
	current, err := h.manager.entryFor(instance.ID)
	if err != nil {
		t.Fatalf("entry: %v", err)
	}
	current.generation.Store(2)
	h.manager.handleQRItem(current, current.emitter(1), 1, QRItem{Kind: QRItemCode, Code: "old", Timeout: time.Minute})
	if _, active := current.currentQR(time.Now()); active {
		t.Fatal("qr item from an old generation must be discarded")
	}
}

func TestDeleteKeepsDeviceWhenTransactionFails(t *testing.T) {
	instance := pairedInstance(domain.InstanceStatusConnected)
	h := newHarness(t, instance)
	h.store.doErr = errBoom
	err := h.manager.Delete(context.Background(), instance.ID)
	if !errors.Is(err, errBoom) {
		t.Fatalf("err = %v", err)
	}
	if h.factory.count() != 0 {
		t.Fatalf("device must not be touched, clients = %d", h.factory.count())
	}
	if len(h.store.deleted) != 0 {
		t.Fatal("instance must not be deleted")
	}
}

func TestDeleteIgnoresDeviceEraseFailure(t *testing.T) {
	instance := pairedInstance(domain.InstanceStatusConnected)
	h := newHarness(t, instance)
	h.factory.prepare = func(client *fakeClient) { client.deleteErr = errBoom }
	if err := h.manager.Delete(context.Background(), instance.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(h.store.deleted) != 1 {
		t.Fatal("instance must be deleted")
	}
}

func TestCallsAfterStopFailWithoutCreatingEntries(t *testing.T) {
	instance := pairedInstance(domain.InstanceStatusDisconnected)
	h := newHarness(t, instance)
	if _, err := h.manager.Connect(context.Background(), instance.ID); err != nil {
		t.Fatalf("connect: %v", err)
	}
	h.manager.Stop()
	if _, err := h.manager.Connect(context.Background(), instance.ID); !errors.Is(err, ErrStopped) {
		t.Fatalf("connect err = %v", err)
	}
	if err := h.manager.reconnect(context.Background(), instance.ID, shouldReconnectOnStartup); !errors.Is(err, ErrStopped) {
		t.Fatalf("reconnect err = %v", err)
	}
	if err := h.manager.Start(context.Background()); !errors.Is(err, ErrStopped) {
		t.Fatalf("start err = %v", err)
	}
	if len(h.manager.entries) != 0 {
		t.Fatalf("entries = %d", len(h.manager.entries))
	}
	if !h.factory.last().called("release") {
		t.Fatal("stop must release clients")
	}
}
