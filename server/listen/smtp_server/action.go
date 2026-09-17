package smtp_server

import (
	"database/sql"
	"errors"
	"github.com/Jinnrry/pmail/config"
	"github.com/Jinnrry/pmail/db"
	"github.com/Jinnrry/pmail/models"
	"github.com/Jinnrry/pmail/utils/context"
	"github.com/Jinnrry/pmail/utils/id"
	"github.com/Jinnrry/pmail/utils/password"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
	log "github.com/sirupsen/logrus"
	"net"
	"strings"
)

// The Backend implements SMTP server methods.
type Backend struct{}

func (bkd *Backend) NewSession(conn *smtp.Conn) (smtp.Session, error) {
	remoteAddress := conn.Conn().RemoteAddr()
	ctx := &context.Context{}
	ctx.SetValue(context.LogID, id.GenLogID())
	log.WithContext(ctx).Debugf("新SMTP连接")
	return &Session{RemoteAddress: remoteAddress, Ctx: ctx}, nil
}

// A Session is returned after EHLO.
type Session struct {
	RemoteAddress net.Addr
	User          string
	From          string
	To            []string
	Ctx           *context.Context
}

func (s *Session) AuthMechanisms() []string {
	return []string{sasl.Plain, sasl.Login}
}

func (s *Session) Auth(mech string) (sasl.Server, error) {
	log.WithContext(s.Ctx).Debugf("Auth :%s", mech)
	if mech == sasl.Plain {
		return sasl.NewPlainServer(func(identity, username, password string) error {
			return s.AuthPlain(username, password)
		}), nil
	}
	if mech == sasl.Login {
		return NewLoginServer(func(username, password string) error {
			return s.AuthPlain(username, password)
		}), nil
	}
	return nil, errors.New("Auth Not Supported")
}

func (s *Session) AuthPlain(username, pwd string) error {
	// Never log SMTP passwords or authorization payloads.
	log.WithContext(s.Ctx).Debugf("Auth attempt username=%s", username)

	lookupAccount := username
	infos := strings.Split(username, "@")
	if len(infos) > 1 {
		lookupAccount = infos[0]
	}

	var user models.User
	encodePwd := password.Encode(pwd)
	_, err := db.Instance.Where("account =? and password =? and disabled=0", lookupAccount, encodePwd).Get(&user)
	if err != nil && err != sql.ErrNoRows {
		log.Errorf("%+v", err)
	}

	if user.ID > 0 {
		s.User = username
		s.Ctx.UserAccount = user.Account
		s.Ctx.UserID = user.ID
		s.Ctx.UserName = user.Name
		s.Ctx.IsAdmin = user.IsAdmin == 1
		log.WithContext(s.Ctx).Debugf("Auth Success account=%s user_id=%d admin=%t", user.Account, user.ID, s.Ctx.IsAdmin)
		return nil
	}

	log.WithContext(s.Ctx).Debugf("Auth failed username=%s", lookupAccount)
	return errors.New("password error")
}

func (s *Session) Mail(from string, opts *smtp.MailOptions) error {
	log.WithContext(s.Ctx).Debugf("Mail From=%s", from)
	s.From = from
	return nil
}

func (s *Session) Rcpt(to string, opts *smtp.RcptOptions) error {
	account, domain, ok := splitSMTPAddress(to)
	if !ok {
		return &smtp.SMTPError{Code: 553, EnhancedCode: smtp.EnhancedCode{5, 1, 3}, Message: "Invalid recipient address"}
	}

	local := isLocalDomain(domain)
	if s.Ctx.UserID <= 0 && !local {
		log.WithContext(s.Ctx).Warnf("Relay denied for unauthenticated recipient: %s", to)
		return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 7, 1}, Message: "Relaying denied"}
	}

	if local {
		var user models.User
		has, err := db.Instance.Where("LOWER(account)=? and disabled=0", strings.ToLower(account)).Get(&user)
		if err != nil {
			log.WithContext(s.Ctx).Errorf("Recipient lookup failed for %s: %v", to, err)
			return &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 3, 0}, Message: "Temporary local recipient lookup failure"}
		}
		if !has || user.ID <= 0 {
			log.WithContext(s.Ctx).Infof("Unknown local recipient rejected at RCPT: %s", to)
			return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 1, 1}, Message: "User unknown"}
		}
	}

	log.WithContext(s.Ctx).Debugf("Rcpt accepted %s", to)
	s.To = append(s.To, to)
	return nil
}

func (s *Session) Reset() {
	s.From = ""
	s.To = nil
}

func (s *Session) Logout() error {
	return nil
}

func splitSMTPAddress(address string) (account, domain string, ok bool) {
	address = strings.TrimSpace(strings.Trim(address, "<>"))
	at := strings.LastIndex(address, "@")
	if at <= 0 || at >= len(address)-1 {
		return "", "", false
	}
	account = strings.TrimSpace(address[:at])
	domain = strings.ToLower(strings.TrimSpace(address[at+1:]))
	return account, domain, account != "" && domain != ""
}

func isLocalDomain(domain string) bool {
	for _, localDomain := range config.Instance.Domains {
		if strings.EqualFold(strings.TrimSpace(localDomain), domain) {
			return true
		}
	}
	return false
}
