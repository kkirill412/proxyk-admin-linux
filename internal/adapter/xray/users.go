package xray

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"proxyk-admin/internal/domain"

	handler "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/uuid"
	"github.com/xtls/xray-core/proxy/vless"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	inboundTag      = "vless-reality"
	userEmailPrefix = "proxyk-user-"
)

type inboundUser struct {
	email   string
	level   uint32
	account *vless.Account
	key     [16]byte
}

// SyncUsers reconciles only the accounts in the reserved ProxyK namespace.
// The caller serializes calls. If an RPC fails, the next call reads Xray again.
func (c *Client) SyncUsers(ctx context.Context, users []domain.VPNUser) error {
	desired, err := desiredUsers(users)
	if err != nil {
		return err
	}
	current, err := c.listInboundUsers(ctx)
	if err != nil {
		return err
	}
	foreignKeys := make(map[[16]byte]bool)
	keyCount := make(map[[16]byte]int)
	for _, user := range current {
		keyCount[user.key]++
		if !managedEmail(user.email) {
			foreignKeys[user.key] = true
		}
	}
	var conflict bool
	for _, user := range current {
		if !managedEmail(user.email) {
			continue
		}
		email := strings.ToLower(user.email)
		wanted, found := desired[email]
		if foreignKeys[user.key] {
			// Xray removes the UUID entry even if a foreign email shares it.
			conflict = true
			delete(desired, email)
			continue
		}
		if found && user.email == email && user.level == 0 && keyCount[user.key] == 1 && proto.Equal(user.account, wanted.account) {
			delete(desired, email)
			continue
		}
		if err := c.alterInbound(ctx, "remove user", &handler.RemoveUserOperation{Email: user.email}); err != nil {
			return err
		}
	}
	for email, user := range desired {
		if foreignKeys[user.key] {
			conflict = true
			continue
		}
		operation := &handler.AddUserOperation{User: &protocol.User{
			Email:   email,
			Level:   0,
			Account: serial.ToTypedMessage(user.account),
		}}
		if err := c.alterInbound(ctx, "add user", operation); err != nil {
			return err
		}
	}
	if conflict {
		return fmt.Errorf("xray: managed VLESS credential conflicts with an unmanaged account")
	}
	return nil
}

func desiredUsers(users []domain.VPNUser) (map[string]inboundUser, error) {
	desired := make(map[string]inboundUser, len(users))
	keys := make(map[[16]byte]bool, len(users))
	for _, user := range users {
		if !user.Enabled {
			continue
		}
		email := vpnUserEmail(user.ID)
		id, err := uuid.ParseString(user.UUID)
		if err != nil || !managedEmail(email) || id.String() != user.UUID {
			return nil, fmt.Errorf("xray: invalid managed VPN account")
		}
		key := vless.ProcessUUID(id)
		if _, found := desired[email]; found || keys[key] {
			return nil, fmt.Errorf("xray: duplicate managed VPN account")
		}
		keys[key] = true
		desired[email] = inboundUser{account: &vless.Account{Id: user.UUID, Flow: domain.VLESSFlow}, key: key}
	}
	return desired, nil
}

func vpnUserEmail(id int64) string {
	return userEmailPrefix + strconv.FormatInt(id, 10)
}

func managedEmail(email string) bool {
	suffix, found := strings.CutPrefix(strings.ToLower(email), userEmailPrefix)
	if !found {
		return false
	}
	id, err := strconv.ParseInt(suffix, 10, 64)
	return err == nil && id > 0 && strconv.FormatInt(id, 10) == suffix
}

func (c *Client) listInboundUsers(ctx context.Context) ([]inboundUser, error) {
	response, err := c.handlers.GetInboundUsers(ctx, &handler.GetInboundUserRequest{Tag: inboundTag})
	if err != nil {
		return nil, rpcError("list users", err)
	}
	users := make([]inboundUser, 0, len(response.GetUsers()))
	for _, user := range response.GetUsers() {
		if user.GetAccount() == nil {
			return nil, fmt.Errorf("xray: missing VLESS account in inbound user list")
		}
		message, err := user.GetAccount().GetInstance()
		account, ok := message.(*vless.Account)
		if err != nil || !ok {
			return nil, fmt.Errorf("xray: invalid VLESS account in inbound user list")
		}
		id, err := uuid.ParseString(account.GetId())
		if err != nil {
			return nil, fmt.Errorf("xray: invalid VLESS credential in inbound user list")
		}
		users = append(users, inboundUser{user.GetEmail(), user.GetLevel(), account, vless.ProcessUUID(id)})
	}
	return users, nil
}

func (c *Client) alterInbound(ctx context.Context, action string, operation proto.Message) error {
	_, err := c.handlers.AlterInbound(ctx, &handler.AlterInboundRequest{
		Tag:       inboundTag,
		Operation: serial.ToTypedMessage(operation),
	})
	if err != nil {
		return rpcError(action, err)
	}
	return nil
}

func rpcError(action string, err error) error {
	// An Xray status message can contain credentials. Keep only its code.
	return fmt.Errorf("xray: %s failed (%s)", action, status.Code(err))
}
