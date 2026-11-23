package whatsapp

import (
	"context"

	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/OBrunoW/catwire/internal/application/ingestion"
)

type lidResolver interface {
	GetPNForLID(ctx context.Context, lid types.JID) (types.JID, error)
	GetLIDForPN(ctx context.Context, pn types.JID) (types.JID, error)
}

type identityKey struct {
	jid types.JID
	alt types.JID
}

type identityPair struct {
	pn  types.JID
	lid types.JID
}

type identityResolver struct {
	lids   lidResolver
	logger waLog.Logger
	cache  map[identityKey]identityPair
}

func newIdentityResolver(lids lidResolver, logger waLog.Logger) *identityResolver {
	return &identityResolver{lids: lids, logger: logger, cache: map[identityKey]identityPair{}}
}

func (r *identityResolver) resolve(ctx context.Context, jid, alt types.JID) identityPair {
	key := identityKey{jid: jid, alt: alt}
	if cached, found := r.cache[key]; found {
		return cached
	}
	pair := r.lookup(ctx, jid, alt)
	r.cache[key] = pair
	return pair
}

func (r *identityResolver) lookup(ctx context.Context, jid, alt types.JID) identityPair {
	switch jid.Server {
	case types.DefaultUserServer:
		pn := jid.ToNonAD()
		if alt.Server == types.HiddenUserServer {
			return identityPair{pn: pn, lid: alt.ToNonAD()}
		}
		return identityPair{pn: pn, lid: r.lidForPN(ctx, pn)}
	case types.HiddenUserServer:
		lid := jid.ToNonAD()
		if alt.Server == types.DefaultUserServer {
			return identityPair{pn: alt.ToNonAD(), lid: lid}
		}
		return identityPair{pn: r.pnForLID(ctx, lid), lid: lid}
	}
	return identityPair{}
}

func (r *identityResolver) lidForPN(ctx context.Context, pn types.JID) types.JID {
	lid, err := r.lids.GetLIDForPN(ctx, pn)
	if err != nil {
		r.logger.Warnf("lid lookup for %s failed: %v", pn, err)
		return types.EmptyJID
	}
	return lid
}

func (r *identityResolver) pnForLID(ctx context.Context, lid types.JID) types.JID {
	pn, err := r.lids.GetPNForLID(ctx, lid)
	if err != nil {
		r.logger.Warnf("pn lookup for %s failed: %v", lid, err)
		return types.EmptyJID
	}
	return pn
}

func (r *identityResolver) chatRef(ctx context.Context, source types.MessageSource, name *string) ingestion.ChatRef {
	if source.Chat.Server == types.GroupServer {
		return ingestion.ChatRef{JID: source.Chat.String(), Group: true, Name: name}
	}
	alt := source.SenderAlt
	if source.IsFromMe {
		alt = source.RecipientAlt
	}
	pair := r.resolve(ctx, source.Chat, alt)
	return directChatRef(pair, source.Chat, name)
}

func directChatRef(pair identityPair, fallback types.JID, name *string) ingestion.ChatRef {
	ref := ingestion.ChatRef{JID: fallback.ToNonAD().String(), Name: name}
	if !pair.lid.IsEmpty() {
		lid := pair.lid.String()
		ref.LID = &lid
		ref.JID = lid
	}
	if !pair.pn.IsEmpty() {
		phone := pair.pn.User
		ref.JID = pair.pn.String()
		ref.Phone = &phone
	}
	return ref
}

func (r *identityResolver) sender(ctx context.Context, info types.MessageInfo) ingestion.Sender {
	pair := r.resolve(ctx, info.Sender, info.SenderAlt)
	ref := directChatRef(pair, info.Sender, nil)
	return ingestion.Sender{
		JID:      ref.JID,
		Phone:    ref.Phone,
		LID:      ref.LID,
		PushName: optionalString(info.PushName),
	}
}

func lightChatRef(source types.MessageSource) ingestion.ChatRef {
	return ingestion.ChatRef{JID: source.Chat.ToNonAD().String(), Group: source.Chat.Server == types.GroupServer}
}

func supportedChat(chat types.JID) bool {
	switch chat.Server {
	case types.DefaultUserServer, types.HiddenUserServer, types.GroupServer:
		return true
	}
	return false
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
