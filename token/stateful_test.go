package token

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

func timeEqual(a, b *time.Time) bool {
	if a == nil && b == nil {
		return true
	}

	if a != nil && b != nil {
		return (*a).Equal(*b)
	}

	return false
}

func equal(a, b *Stateful) bool {
	if a.Token != b.Token || a.Group != b.Group ||
		!reflect.DeepEqual(a.Username, b.Username) ||
		!reflect.DeepEqual(a.Permissions, b.Permissions) ||
		!timeEqual(a.Expires, b.Expires) ||
		!reflect.DeepEqual(a.IssuedBy, b.IssuedBy) ||
		!timeEqual(a.IssuedAt, b.IssuedAt) {
		return false
	}

	return true
}

func TestStatefulCheck(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Hour)
	nearFuture := now.Add(time.Hour / 2)
	future := now.Add(time.Hour)
	user := "user"
	user2 := "user2"
	token1 := &Stateful{
		Token:       "token",
		Group:       "group",
		Username:    &user,
		Permissions: []string{"present", "message"},
		Expires:     &future,
	}
	token2 := &Stateful{
		Token:       "token",
		Group:       "group",
		Permissions: []string{"present", "message"},
		Expires:     &future,
	}
	token3 := &Stateful{
		Token:       "token",
		Group:       "group",
		Username:    &user,
		Permissions: []string{"present", "message"},
		Expires:     &past,
	}
	token4 := &Stateful{
		Token:       "token",
		Group:       "group",
		Username:    &user,
		Permissions: []string{"present", "message"},
		Expires:     &future,
		NotBefore:   &nearFuture,
	}
	token5 := &Stateful{
		Token:            "token",
		Group:            "group",
		IncludeSubgroups: true,
		Username:         &user,
		Permissions:      []string{"present", "message"},
		Expires:          &future,
	}

	success := []struct {
		token          *Stateful
		group          string
		username       *string
		expUsername    string
		expPermissions []string
	}{
		{
			token:          token1,
			group:          "group",
			username:       &user,
			expUsername:    user,
			expPermissions: []string{"present", "message"},
		},
		{
			token:          token1,
			group:          "group",
			username:       &user2,
			expUsername:    user,
			expPermissions: []string{"present", "message"},
		},
		{
			token:          token1,
			group:          "group",
			expUsername:    user,
			expPermissions: []string{"present", "message"},
		},
		{
			token:          token2,
			group:          "group",
			username:       &user,
			expUsername:    "",
			expPermissions: []string{"present", "message"},
		},
		{
			token:          token5,
			group:          "group",
			username:       &user,
			expUsername:    "user",
			expPermissions: []string{"present", "message"},
		},
		{
			token:          token5,
			group:          "group/subgroup",
			username:       &user,
			expUsername:    "user",
			expPermissions: []string{"present", "message"},
		},
	}

	for i, s := range success {
		u, p, err := s.token.Check("", s.group, s.username)
		if err != nil || u != s.expUsername ||
			!reflect.DeepEqual(p, s.expPermissions) {
			t.Errorf("Check %v failed: %v %v %v -> %v %v %v",
				i, s.token, s.group, s.username,
				u, p, err)
		}
	}

	failure := []struct {
		token    *Stateful
		group    string
		username *string
	}{
		{
			token:    token1,
			group:    "group2",
			username: &user,
		},
		{
			token:    token3,
			group:    "group",
			username: &user,
		},
		{
			token:    token4,
			group:    "group",
			username: &user,
		},
		{
			token:    token5,
			group:    "groups",
			username: &user,
		},
	}

	for i, s := range failure {
		u, p, err := s.token.Check("", s.group, s.username)
		if err == nil {
			t.Errorf("Check %v succeded: %v %v %v -> %v %v %v",
				i, s.token, s.group, s.username,
				u, p, err)
		}
	}
}

func readTokenFile(filename string) []*Stateful {
	f, err := os.Open(filename)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	a := make([]*Stateful, 0)
	decoder := json.NewDecoder(f)
	for {
		var t Stateful
		err := decoder.Decode(&t)
		if err == io.EOF {
			break
		} else if err != nil {
			panic(err)
		}
		a = append(a, &t)
	}
	return a
}

func expectTokenArray(t *testing.T, a, b []*Stateful) {
	if len(a) != len(b) {
		t.Errorf("Bad length: %v != %v", len(a), len(b))
	}
	aa := append([]*Stateful(nil), a...)
	sort.Slice(aa, func(i, j int) bool {
		return aa[i].Token < aa[j].Token
	})
	bb := append([]*Stateful(nil), b...)
	sort.Slice(bb, func(i, j int) bool {
		return bb[i].Token < bb[j].Token
	})

	if len(aa) != len(bb) {
		t.Errorf("Not equal: %v != %v", len(aa), len(bb))
	}

	for i, ta := range aa {
		tb := bb[i]
		if !equal(ta, tb) {
			t.Errorf("Not equal: %v != %v", ta, tb)
		}
	}
}

func expectTokens(t *testing.T, tokens map[string]*Stateful, value []*Stateful) {
	a := make([]*Stateful, 0, len(tokens))
	for tok, token := range tokens {
		if tok != token.Token {
			t.Errorf("Inconsistent token: %v != %v",
				tok, token.Token)
		}
		a = append(a, token)
	}
	expectTokenArray(t, a, value)
}

func expectTokenFile(t *testing.T, filename string, value []*Stateful) {
	a := readTokenFile(filename)
	expectTokenArray(t, a, value)
}

func TestTokenStorage(t *testing.T) {
	d := t.TempDir()
	s := state{
		filename: filepath.Join(d, "test.jsonl"),
	}
	now := time.Now()
	past := now.Add(-time.Hour)
	nearFuture := now.Add(time.Hour / 2)
	future := now.Add(time.Hour)
	user1 := "user1"
	user2 := "user2"
	user3 := "user3"
	tokens := []*Stateful{
		{
			Token:       "tok1",
			Group:       "test",
			Username:    &user1,
			Permissions: []string{"present", "message"},
			Expires:     &future,
		},
		{
			Token:       "tok2",
			Group:       "test",
			Username:    &user2,
			Permissions: []string{"present", "record", "message"},
			Expires:     &nearFuture,
			NotBefore:   &past,
		},
		{
			Token:       "tok3",
			Group:       "test",
			Username:    &user3,
			Permissions: []string{"present", "message"},
			Expires:     &nearFuture,
		},
	}
	for i, token := range tokens {
		new, err := s.Update(token, "")
		if err != nil {
			t.Errorf("Add: %v", err)
		}
		if !equal(new, token) {
			t.Errorf("Add: got %v, expected %v", new, token)
		}
		expectTokens(t, s.tokens, tokens[:i+1])
		expectTokenFile(t, s.filename, tokens[:i+1])
	}

	s.modTime = time.Time{}
	_, err := s.load()
	if err != nil {
		t.Errorf("Load: %v", err)
	}
	expectTokens(t, s.tokens, tokens)

	t1, etag, err := s.Get("tok2")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	t2 := t1.Clone()
	soon := now.Add(time.Hour)
	t2.Expires = &soon
	_, err = s.Update(t2, "")
	if !errors.Is(err, ErrTagMismatch) {
		t.Errorf("Update: got %v, expected ErrTagMismatch", err)
	}
	_, err = s.Update(t2, "\"bad\"")
	if !errors.Is(err, ErrTagMismatch) {
		t.Errorf("Update: got %v, expected ErrTagMismatch", err)
	}
	new, err := s.Update(t2, etag)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	tokens[1].Expires = &future
	if !equal(new, tokens[1]) {
		t.Errorf("Edit: got %v, expected %v", tokens[1], new)
	}
	expectTokens(t, s.tokens, tokens)
	expectTokenFile(t, s.filename, tokens)

	for t := range s.tokens {
		delete(s.tokens, t)
	}

	err = s.rewrite()
	if err != nil {
		t.Errorf("rewrite(empty): %v", err)
	}

	_, err = os.Stat(s.filename)
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("existence check: %v", err)
	}
}

func TestExpire(t *testing.T) {
	d := t.TempDir()
	s := state{
		filename: filepath.Join(d, "test.jsonl"),
	}
	now := time.Now()
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour * 24 * 6)
	longPast := now.Add(-time.Hour * 24 * 8)
	user := "user"

	tokens := []*Stateful{
		{
			Token:       "tok1",
			Group:       "test",
			Username:    &user,
			Permissions: []string{"present", "message"},
			Expires:     &now,
		},
		{
			Token:       "tok2",
			Group:       "test",
			Username:    &user,
			Permissions: []string{"present", "message"},
			Expires:     &future,
		},
		{
			Token:       "tok3",
			Group:       "test",
			Username:    &user,
			Permissions: []string{"present", "message"},
			Expires:     &now,
		},
		{
			Token:       "tok4",
			Group:       "test",
			Username:    &user,
			Permissions: []string{"present", "message"},
			Expires:     &past,
		},
		{
			Token:       "tok5",
			Group:       "test",
			Username:    &user,
			Permissions: []string{"present", "message"},
			Expires:     &longPast,
		},
	}

	for _, token := range tokens {
		_, err := s.Update(token, "")
		if err != nil {
			t.Errorf("Add: %v", err)
		}
	}

	expectTokens(t, s.tokens, tokens)
	expectTokenFile(t, s.filename, tokens)

	err := s.Expire()
	if err != nil {
		t.Errorf("Expire: %v", err)
	}

	expectTokens(t, s.tokens, tokens[:len(tokens)-1])
	expectTokenFile(t, s.filename, tokens[:len(tokens)-1])
}
