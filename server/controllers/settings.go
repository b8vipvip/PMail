package controllers

import (
	"encoding/json"
	"github.com/Jinnrry/pmail/db"
	"github.com/Jinnrry/pmail/dto/response"
	"github.com/Jinnrry/pmail/i18n"
	"github.com/Jinnrry/pmail/services/outbound"
	"github.com/Jinnrry/pmail/utils/context"
	"github.com/Jinnrry/pmail/utils/password"
	log "github.com/sirupsen/logrus"
	"io"
	"net/http"
)

type modifyPasswordRequest struct {
	Password string `json:"password"`
}

type outboundSettingsRequest struct {
	Mode     string `json:"mode"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	Security string `json:"security"`
}

func ModifyPassword(ctx *context.Context, w http.ResponseWriter, req *http.Request) {
	reqBytes, err := io.ReadAll(req.Body)
	if err != nil {
		log.Errorf("%+v", err)
	}
	var retData modifyPasswordRequest
	err = json.Unmarshal(reqBytes, &retData)
	if err != nil {
		log.Errorf("%+v", err)
	}

	if retData.Password != "" {
		encodePwd := password.Encode(retData.Password)
		_, err := db.Instance.Table("user").Where("id=?", ctx.UserID).Update(map[string]interface{}{"password": encodePwd})
		if err != nil {
			response.NewErrorResponse(response.ServerError, i18n.GetText(ctx.Lang, "unknowError"), "").FPrint(w)
			return
		}
	}

	response.NewSuccessResponse(i18n.GetText(ctx.Lang, "succ")).FPrint(w)
}

// OutboundSettings returns or updates the outbound delivery configuration.
// Relay credentials are admin-only and the saved password is never returned.
func OutboundSettings(ctx *context.Context, w http.ResponseWriter, req *http.Request) {
	if !ctx.IsAdmin {
		response.NewErrorResponse(response.NoAccessPrivileges, "admin required", "").FPrint(w)
		return
	}

	switch req.Method {
	case http.MethodGet:
		cfg, err := outbound.GetPublic()
		if err != nil {
			log.WithContext(ctx).Errorf("read outbound settings: %v", err)
			response.NewErrorResponse(response.ServerError, "read outbound settings failed", err.Error()).FPrint(w)
			return
		}
		response.NewSuccessResponse(cfg).FPrint(w)
		return

	case http.MethodPost:
		body, err := io.ReadAll(req.Body)
		if err != nil {
			response.NewErrorResponse(response.ParamsError, "params error", err.Error()).FPrint(w)
			return
		}
		var input outboundSettingsRequest
		if err = json.Unmarshal(body, &input); err != nil {
			response.NewErrorResponse(response.ParamsError, "params error", err.Error()).FPrint(w)
			return
		}

		next := outbound.Config{
			Mode:     input.Mode,
			Host:     input.Host,
			Port:     input.Port,
			Username: input.Username,
			Password: input.Password,
			Security: input.Security,
		}
		// An empty password means "keep the existing secret". This lets the UI
		// edit host/port/security without ever reading the stored password back.
		if err = outbound.Save(next, true); err != nil {
			response.NewErrorResponse(response.ParamsError, "invalid outbound settings", err.Error()).FPrint(w)
			return
		}
		cfg, err := outbound.GetPublic()
		if err != nil {
			response.NewErrorResponse(response.ServerError, "read outbound settings failed", err.Error()).FPrint(w)
			return
		}
		log.WithContext(ctx).Infof("Outbound settings updated: mode=%s host=%s port=%d security=%s username_set=%t password_set=%t", cfg.Mode, cfg.Host, cfg.Port, cfg.Security, cfg.Username != "", cfg.PasswordSet)
		response.NewSuccessResponse(cfg).FPrint(w)
		return

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		response.NewErrorResponse(response.ParamsError, "method not allowed", "").FPrint(w)
	}
}
