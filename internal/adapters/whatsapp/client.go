package whatsapp

import (
	"context"
	"errors"
	"fmt"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/OBrunoW/catwire/internal/application/session"
	"github.com/OBrunoW/catwire/internal/domain"
)

const (
	pairDisplayName = "Chrome (Linux)"
)

type Factory struct {
	container *sqlstore.Container
	logger    waLog.Logger
}

func NewFactory(store *Store, logger waLog.Logger) *Factory {
	return &Factory{container: store.container, logger: logger}
}

func (f *Factory) Create(ctx context.Context, instance domain.Instance, emit func(session.Event)) (session.Client, error) {
	device, err := f.loadDevice(ctx, instance)
	if err != nil {
		return nil, err
	}
	raw := whatsmeow.NewClient(device, f.logger)
	raw.AddEventHandler(func(event any) {
		if translated, relevant := translateEvent(event); relevant {
			emit(translated)
		}
	})
	return &client{raw: raw}, nil
}

func (f *Factory) loadDevice(ctx context.Context, instance domain.Instance) (*store.Device, error) {
	if instance.JID == nil {
		return f.container.NewDevice(), nil
	}
	jid, err := types.ParseJID(*instance.JID)
	if err != nil {
		return nil, fmt.Errorf("whatsapp: parse instance jid: %w", err)
	}
	device, err := f.container.GetDevice(ctx, jid)
	if err != nil {
		return nil, fmt.Errorf("whatsapp: load device: %w", err)
	}
	if device == nil {
		return nil, session.ErrDeviceNotFound
	}
	return device, nil
}

type client struct {
	raw *whatsmeow.Client
}

func (c *client) Connect() error {
	err := c.raw.Connect()
	if errors.Is(err, whatsmeow.ErrAlreadyConnected) {
		return nil
	}
	return err
}

func (c *client) Disconnect() {
	c.raw.Disconnect()
}

func (c *client) Logout(ctx context.Context) error {
	return c.raw.Logout(ctx)
}

func (c *client) DeleteDevice(ctx context.Context) error {
	return c.raw.Store.Delete(ctx)
}

func (c *client) IsConnected() bool {
	return c.raw.IsConnected()
}

func (c *client) PairPhone(ctx context.Context, phone string) (string, error) {
	return c.raw.PairPhone(ctx, phone, true, whatsmeow.PairClientChrome, pairDisplayName)
}

func (c *client) Release() {
	c.raw.RemoveEventHandlers()
	c.raw.Disconnect()
}

func (c *client) OpenQRChannel(ctx context.Context) (<-chan session.QRItem, error) {
	source, err := c.raw.GetQRChannel(ctx)
	if err != nil {
		return nil, err
	}
	output := make(chan session.QRItem)
	go forwardQRItems(ctx, source, output)
	return output, nil
}

func forwardQRItems(ctx context.Context, source <-chan whatsmeow.QRChannelItem, output chan<- session.QRItem) {
	defer close(output)
	for {
		select {
		case <-ctx.Done():
			return
		case item, open := <-source:
			if !open {
				return
			}
			select {
			case output <- translateQRItem(item):
			case <-ctx.Done():
				return
			}
		}
	}
}
