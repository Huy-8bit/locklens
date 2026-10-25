package onboard

import (
	"errors"
	"net/url"
	"strings"
)

// WithCredentials returns dsn with its user and password replaced. Both the
// URL form (postgres://…) and the key/value form (host=… user=…) are
// supported; every other parameter (host, port, database, sslmode, …) is
// kept as written.
func WithCredentials(dsn, user, password string) (string, error) {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return "", errors.New("the connection string is not a valid URL")
		}
		u.User = url.UserPassword(user, password)
		q := u.Query()
		q.Del("user")
		q.Del("password")
		u.RawQuery = q.Encode()
		return u.String(), nil
	}
	pairs, err := parseKeyValue(dsn)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, p := range pairs {
		if p[0] == "user" || p[0] == "password" {
			continue
		}
		writePair(&b, p[0], p[1])
	}
	writePair(&b, "user", user)
	writePair(&b, "password", password)
	return strings.TrimSpace(b.String()), nil
}

func writePair(b *strings.Builder, k, v string) {
	b.WriteString(k)
	b.WriteString("='")
	b.WriteString(strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(v))
	b.WriteString("' ")
}

// parseKeyValue splits a libpq key/value connection string. Values may be
// single-quoted, with \' and \\ escapes.
func parseKeyValue(s string) ([][2]string, error) {
	var out [][2]string
	i := 0
	skip := func() {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
			i++
		}
	}
	for {
		skip()
		if i >= len(s) {
			return out, nil
		}
		start := i
		for i < len(s) && s[i] != '=' && s[i] != ' ' && s[i] != '\t' {
			i++
		}
		key := s[start:i]
		skip()
		if key == "" || i >= len(s) || s[i] != '=' {
			return nil, errors.New("the connection string is neither a postgres:// URL nor key=value pairs")
		}
		i++
		skip()
		var val strings.Builder
		if i < len(s) && s[i] == '\'' {
			i++
			closed := false
			for i < len(s) {
				c := s[i]
				if c == '\\' && i+1 < len(s) {
					val.WriteByte(s[i+1])
					i += 2
					continue
				}
				if c == '\'' {
					i++
					closed = true
					break
				}
				val.WriteByte(c)
				i++
			}
			if !closed {
				return nil, errors.New("unterminated quoted value in the connection string")
			}
		} else {
			for i < len(s) && s[i] != ' ' && s[i] != '\t' && s[i] != '\n' && s[i] != '\r' {
				if s[i] == '\\' && i+1 < len(s) {
					i++
				}
				val.WriteByte(s[i])
				i++
			}
		}
		out = append(out, [2]string{key, val.String()})
	}
}
