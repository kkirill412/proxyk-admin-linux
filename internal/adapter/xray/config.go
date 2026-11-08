package xray

import (
	"encoding/json"
	"proxyk-admin/internal/domain"
	"proxyk-admin/internal/service"
)

// WriteConfig installs an empty VLESS inbound. User management belongs to stage 2.
// Never rewrite this file from the server's ordinary startup path.
func WriteConfig(path string, settings domain.ServerSettings) error {
	config := map[string]any{
		"log":    map[string]any{"loglevel": "warning", "access": "none"},
		"api":    map[string]any{"tag": "api", "listen": "0.0.0.0:10085", "services": []string{"HandlerService", "StatsService"}},
		"stats":  map[string]any{},
		"policy": map[string]any{"levels": map[string]any{"0": map[string]any{"statsUserUplink": true, "statsUserDownlink": true}}},
		"inbounds": []any{map[string]any{
			"tag": "vless-reality", "listen": "0.0.0.0", "port": 8444, "protocol": "vless",
			"settings": map[string]any{"clients": []any{}, "decryption": "none"},
			"streamSettings": map[string]any{
				"network": "raw", "security": "reality",
				"realitySettings": map[string]any{
					"show": false, "target": settings.ServerName + ":443", "xver": 0,
					"serverNames": []string{settings.ServerName}, "privateKey": settings.RealityPrivateKey,
					"shortIds": []string{settings.ShortID},
				},
			},
		}},
		"outbounds": []any{map[string]any{"tag": "direct", "protocol": "freedom"}},
	}
	body, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return service.WritePrivate(path, body)
}
