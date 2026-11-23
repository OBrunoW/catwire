package session

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const eventBufferSize = 64

type QRCode struct {
	Code      string
	ExpiresAt time.Time
}

type queuedEvent struct {
	generation uint64
	event      Event
}

type entry struct {
	instanceID uuid.UUID
	ctx        context.Context
	cancel     context.CancelFunc
	events     chan queuedEvent

	mu           sync.Mutex
	client       Client
	clientCtx    context.Context
	clientCancel context.CancelFunc
	dropped      bool
	stopped      bool
	generation   atomic.Uint64

	qrMu       sync.Mutex
	qr         *QRCode
	qrFirst    chan struct{}
	qrSignaled bool
}

func newEntry(parent context.Context, instanceID uuid.UUID) *entry {
	ctx, cancel := context.WithCancel(parent)
	return &entry{
		instanceID: instanceID,
		ctx:        ctx,
		cancel:     cancel,
		events:     make(chan queuedEvent, eventBufferSize),
		qrFirst:    make(chan struct{}),
	}
}

func (e *entry) emitter(generation uint64) func(Event) {
	return func(event Event) {
		select {
		case e.events <- queuedEvent{generation: generation, event: event}:
		case <-e.ctx.Done():
		}
	}
}

func (e *entry) unavailable() bool {
	return e.dropped || e.stopped
}

func (e *entry) setQR(generation uint64, code string, expiresAt time.Time) {
	e.qrMu.Lock()
	defer e.qrMu.Unlock()
	if e.generation.Load() != generation {
		return
	}
	e.qr = &QRCode{Code: code, ExpiresAt: expiresAt}
	if e.qrSignaled {
		return
	}
	e.qrSignaled = true
	close(e.qrFirst)
}

func (e *entry) currentQR(now time.Time) (QRCode, bool) {
	e.qrMu.Lock()
	defer e.qrMu.Unlock()
	if e.qr == nil || !e.qr.ExpiresAt.After(now) {
		return QRCode{}, false
	}
	return *e.qr, true
}

func (e *entry) resetQR() {
	e.qrMu.Lock()
	e.qr = nil
	e.qrFirst = make(chan struct{})
	e.qrSignaled = false
	e.qrMu.Unlock()
}

func (e *entry) firstQRSignal() <-chan struct{} {
	e.qrMu.Lock()
	defer e.qrMu.Unlock()
	return e.qrFirst
}

func (e *entry) releaseClient() {
	if e.client == nil {
		return
	}
	e.generation.Add(1)
	e.clientCancel()
	e.client.Release()
	e.client = nil
	e.clientCtx = nil
	e.clientCancel = nil
	e.resetQR()
}
