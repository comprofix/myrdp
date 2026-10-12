// Package core is the application logic behind the UI: editing the
// connection tree, resolving inherited settings, and keeping the store and
// the keyring in step with it.
package core

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"myrdp/internal/model"
	"myrdp/internal/secrets"
	"myrdp/internal/session"
	"myrdp/internal/store"
)

// TreeNode is a node as the tree view needs it.
type TreeNode struct {
	ID       string      `json:"id"`
	Type     string      `json:"type"`
	Name     string      `json:"name"`
	Host     string      `json:"host,omitempty"`
	Children []*TreeNode `json:"children"`
}

// FieldValue is one setting as the settings view needs it.
type FieldValue struct {
	Field model.Field `json:"field"`
	Value any         `json:"value"`
	// Overridden is true when this node sets the value itself.
	Overridden bool `json:"overridden"`
	// Inherited is the value the node would have without its own override,
	// and Source names where that value comes from.
	Inherited any    `json:"inherited"`
	Source    string `json:"source"`
}

// NodeSettings is everything the settings view shows for one node.
type NodeSettings struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	IsRoot bool   `json:"isRoot"`
	// Parent is the name of the containing folder, empty at the top level.
	Parent string       `json:"parent"`
	Fields []FieldValue `json:"fields"`
}

type Service struct {
	mu      sync.Mutex
	doc     *model.Document
	store   store.Store
	secrets secrets.Store
	index   map[string]*model.Node
}

// New loads the document from st.
func New(st store.Store, sec secrets.Store) (*Service, error) {
	doc, err := st.Load()
	if err != nil {
		return nil, err
	}
	s := &Service{doc: doc, store: st, secrets: sec, index: map[string]*model.Node{}}
	doc.Root.Walk(func(n *model.Node) { s.index[n.ID] = n })
	return s, nil
}

func (s *Service) node(id string) (*model.Node, error) {
	if id == "" {
		id = model.RootID
	}
	n := s.index[id]
	if n == nil {
		return nil, fmt.Errorf("no connection or folder with id %q", id)
	}
	return n, nil
}

func toTreeNode(n *model.Node) *TreeNode {
	t := &TreeNode{ID: n.ID, Type: string(n.Type), Name: n.Name, Children: []*TreeNode{}}
	if h, ok := n.Settings[model.KeyHost].(string); ok {
		t.Host = h
	}
	for _, c := range n.Children {
		t.Children = append(t.Children, toTreeNode(c))
	}
	return t
}

// Tree returns the top level of the tree.
func (s *Service) Tree() []*TreeNode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return toTreeNode(s.doc.Root).Children
}

// Create adds a folder or connection. If parentID names a connection, the
// new node becomes its next sibling.
func (s *Service) Create(parentID, nodeType, name string) (*TreeNode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	parent, err := s.node(parentID)
	if err != nil {
		return nil, err
	}
	typ := model.NodeType(nodeType)
	if typ != model.Folder && typ != model.Connection {
		return nil, fmt.Errorf("unknown node type %q", nodeType)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("name is required")
	}
	index := len(parent.Children)
	if parent.Type == model.Connection {
		index = parent.Index() + 1
		parent = parent.Parent
	}
	n := &model.Node{ID: model.NewID(), Type: typ, Name: name, Settings: map[string]any{}}
	n.Touch()
	attach(n, parent, index)
	s.index[n.ID] = n
	if err := s.store.SaveNode(n); err != nil {
		return nil, err
	}
	return toTreeNode(n), nil
}

func attach(n, parent *model.Node, index int) {
	if index < 0 || index > len(parent.Children) {
		index = len(parent.Children)
	}
	parent.Children = append(parent.Children, nil)
	copy(parent.Children[index+1:], parent.Children[index:])
	parent.Children[index] = n
	n.Parent = parent
}

func detach(n *model.Node) {
	if i := n.Index(); i >= 0 {
		n.Parent.Children = append(n.Parent.Children[:i], n.Parent.Children[i+1:]...)
	}
	n.Parent = nil
}

func (s *Service) Rename(id, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.node(id)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("name is required")
	}
	if n == s.doc.Root {
		return errors.New("the defaults cannot be renamed")
	}
	n.Name = name
	n.Touch()
	return s.store.SaveNode(n)
}

// Delete removes a node, everything below it, and their stored passwords.
func (s *Service) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.node(id)
	if err != nil {
		return err
	}
	if n == s.doc.Root {
		return errors.New("the defaults cannot be deleted")
	}
	detach(n)
	n.Walk(func(c *model.Node) {
		delete(s.index, c.ID)
		for i := range model.Fields {
			if f := &model.Fields[i]; f.Type == model.Secret && c.Settings[f.Key] == true {
				s.secrets.Delete(secrets.Key(c.ID, f.Key))
			}
		}
	})
	return s.store.DeleteNode(id)
}

// Move puts a node at index within a new parent folder. A negative index
// appends.
func (s *Service) Move(id, parentID string, index int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.node(id)
	if err != nil {
		return err
	}
	parent, err := s.node(parentID)
	if err != nil {
		return err
	}
	if n == s.doc.Root {
		return errors.New("the defaults cannot be moved")
	}
	if parent.Type != model.Folder {
		return errors.New("can only move into a folder")
	}
	if parent.IsDescendantOf(n) {
		return errors.New("cannot move a folder into itself")
	}
	if n.Parent == parent && index > n.Index() {
		index-- // removing n first shifts later siblings down
	}
	detach(n)
	attach(n, parent, index)
	n.Touch()
	return s.store.MoveNode(n)
}

// Duplicate copies a node, its descendants and their stored passwords, and
// places the copy after the original.
func (s *Service) Duplicate(id string) (*TreeNode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.node(id)
	if err != nil {
		return nil, err
	}
	if n == s.doc.Root {
		return nil, errors.New("the defaults cannot be duplicated")
	}
	var clone func(src *model.Node) (*model.Node, error)
	clone = func(src *model.Node) (*model.Node, error) {
		dst := &model.Node{ID: model.NewID(), Type: src.Type, Name: src.Name, Settings: map[string]any{}}
		for k, v := range src.Settings {
			dst.Settings[k] = v
		}
		for i := range model.Fields {
			f := &model.Fields[i]
			if f.Type != model.Secret || src.Settings[f.Key] != true {
				continue
			}
			pw, err := s.secrets.Get(secrets.Key(src.ID, f.Key))
			if err == nil {
				err = s.secrets.Set(secrets.Key(dst.ID, f.Key), pw)
			}
			if err != nil {
				return nil, fmt.Errorf("copy password: %w", err)
			}
		}
		dst.Touch()
		for _, c := range src.Children {
			cc, err := clone(c)
			if err != nil {
				return nil, err
			}
			cc.Parent = dst
			dst.Children = append(dst.Children, cc)
		}
		return dst, nil
	}
	dup, err := clone(n)
	if err != nil {
		return nil, err
	}
	dup.Name = n.Name + " (copy)"
	attach(dup, n.Parent, n.Index()+1)
	dup.Walk(func(c *model.Node) { s.index[c.ID] = c })
	if err := s.store.SaveNode(dup); err != nil {
		return nil, err
	}
	return toTreeNode(dup), nil
}

func sourceName(src *model.Node) string {
	switch {
	case src == nil:
		return "built-in default"
	case src.ID == model.RootID:
		return "Defaults"
	}
	return src.Name
}

// Settings returns every setting that applies to a node, with its effective
// value and where that value comes from.
func (s *Service) Settings(id string) (*NodeSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.node(id)
	if err != nil {
		return nil, err
	}
	return s.settingsFor(n), nil
}

// DraftSettings returns the settings a new node of nodeType would start
// with under parentID, without creating it.
func (s *Service) DraftSettings(parentID, nodeType string) (*NodeSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	parent, _, err := s.newNodePlace(parentID)
	if err != nil {
		return nil, err
	}
	typ := model.NodeType(nodeType)
	if typ != model.Folder && typ != model.Connection {
		return nil, fmt.Errorf("unknown node type %q", nodeType)
	}
	draft := &model.Node{Type: typ, Settings: map[string]any{}, Parent: parent}
	return s.settingsFor(draft), nil
}

// newNodePlace resolves where a new node goes: inside a folder, or as the
// next sibling when parentID names a connection.
func (s *Service) newNodePlace(parentID string) (parent *model.Node, index int, err error) {
	parent, err = s.node(parentID)
	if err != nil {
		return nil, 0, err
	}
	if parent.Type == model.Connection {
		return parent.Parent, parent.Index() + 1, nil
	}
	return parent, len(parent.Children), nil
}

func (s *Service) settingsFor(n *model.Node) *NodeSettings {
	out := &NodeSettings{ID: n.ID, Name: n.Name, Type: string(n.Type), IsRoot: n == s.doc.Root, Fields: []FieldValue{}}
	if n.Parent != nil && n.Parent != s.doc.Root {
		out.Parent = n.Parent.Name
	}
	for i := range model.Fields {
		f := &model.Fields[i]
		if f.ConnectionOnly && n.Type != model.Connection {
			continue
		}
		value, src := f.Resolve(n)
		fv := FieldValue{Field: *f, Value: value, Overridden: src == n, Inherited: f.Default, Source: sourceName(nil)}
		if n.Parent != nil && !f.ConnectionOnly {
			var from *model.Node
			fv.Inherited, from = f.Resolve(n.Parent)
			fv.Source = sourceName(from)
		}
		out.Fields = append(out.Fields, fv)
	}
	return out
}

func (s *Service) field(n *model.Node, key string) (*model.Field, error) {
	f := model.FieldFor(key)
	if f == nil {
		return nil, fmt.Errorf("unknown setting %q", key)
	}
	if f.ConnectionOnly && n.Type != model.Connection {
		return nil, fmt.Errorf("%s only applies to connections", f.Label)
	}
	return f, nil
}

// SaveRequest is one Save from the settings view: every change the user
// made, applied together or not at all.
type SaveRequest struct {
	// ID is the node to change; empty creates a new node.
	ID string `json:"id"`
	// ParentID and Type say where and what to create. Ignored for changes.
	ParentID string `json:"parentId"`
	Type     string `json:"type"`
	// Name is the new name; empty keeps an existing node's name.
	Name string `json:"name"`
	// Set holds overrides to store, Reset the keys to inherit again, and
	// Secrets the passwords to put in the keyring.
	Set     map[string]any    `json:"set"`
	Reset   []string          `json:"reset"`
	Secrets map[string]string `json:"secrets"`
}

// Save applies a SaveRequest. Everything is validated before anything is
// changed.
func (s *Service) Save(req SaveRequest) (*TreeNode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var n, parent *model.Node
	var index int
	var err error
	creating := req.ID == ""
	if creating {
		if parent, index, err = s.newNodePlace(req.ParentID); err != nil {
			return nil, err
		}
		typ := model.NodeType(req.Type)
		if typ != model.Folder && typ != model.Connection {
			return nil, fmt.Errorf("unknown node type %q", req.Type)
		}
		n = &model.Node{ID: model.NewID(), Type: typ, Settings: map[string]any{}}
	} else if n, err = s.node(req.ID); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(req.Name)
	switch {
	case name == "" && creating:
		return nil, errors.New("name is required")
	case name != "" && n == s.doc.Root:
		return nil, errors.New("the defaults cannot be renamed")
	}

	set := map[string]any{}
	for key, value := range req.Set {
		f, err := s.field(n, key)
		if err != nil {
			return nil, err
		}
		if f.Type == model.Secret {
			return nil, fmt.Errorf("%s must be saved as a password", f.Label)
		}
		v, err := f.Coerce(value)
		if err != nil {
			return nil, err
		}
		if str, ok := v.(string); ok {
			v = strings.TrimSpace(str)
		}
		set[key] = v
	}
	for _, key := range req.Reset {
		if _, err := s.field(n, key); err != nil {
			return nil, err
		}
	}
	for key, value := range req.Secrets {
		f, err := s.field(n, key)
		if err != nil {
			return nil, err
		}
		if f.Type != model.Secret {
			return nil, fmt.Errorf("%s is not a password", f.Label)
		}
		if value == "" {
			return nil, errors.New("password is empty")
		}
	}

	// The keyring goes first: if it fails, the file hasn't been touched.
	for key, value := range req.Secrets {
		if err := s.secrets.Set(secrets.Key(n.ID, key), value); err != nil {
			return nil, fmt.Errorf("save password to keyring: %w", err)
		}
	}
	for _, key := range req.Reset {
		if model.FieldFor(key).Type == model.Secret && n.Settings[key] == true {
			if err := s.secrets.Delete(secrets.Key(n.ID, key)); err != nil {
				return nil, fmt.Errorf("remove password from keyring: %w", err)
			}
		}
		delete(n.Settings, key)
	}
	for key, v := range set {
		n.Settings[key] = v
	}
	for key := range req.Secrets {
		n.Settings[key] = true
	}
	if name != "" {
		n.Name = name
	}
	if creating {
		attach(n, parent, index)
		s.index[n.ID] = n
	}
	n.Touch()
	if err := s.store.SaveNode(n); err != nil {
		return nil, err
	}
	return toTreeNode(n), nil
}

// Set overrides one setting on a node.
func (s *Service) Set(id, key string, value any) error {
	_, err := s.Save(SaveRequest{ID: s.orRoot(id), Set: map[string]any{key: value}})
	return err
}

// Reset removes a node's override so the setting is inherited again. For a
// password it also removes the stored secret.
func (s *Service) Reset(id, key string) error {
	_, err := s.Save(SaveRequest{ID: s.orRoot(id), Reset: []string{key}})
	return err
}

// SetSecret stores a password in the keyring and marks it on the node.
func (s *Service) SetSecret(id, key, value string) error {
	_, err := s.Save(SaveRequest{ID: s.orRoot(id), Secrets: map[string]string{key: value}})
	return err
}

func (s *Service) orRoot(id string) string {
	if id == "" {
		return model.RootID
	}
	return id
}

func (s *Service) secret(n *model.Node, key string) (string, error) {
	stored, src := model.FieldFor(key).Resolve(n)
	if stored != true || src == nil {
		return "", nil
	}
	pw, err := s.secrets.Get(secrets.Key(src.ID, key))
	if err != nil {
		return "", fmt.Errorf("read password from keyring: %w", err)
	}
	return pw, nil
}

// SessionConfig resolves everything needed to connect to a node.
func (s *Service) SessionConfig(id string) (session.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.node(id)
	if err != nil {
		return session.Config{}, err
	}
	if n.Type != model.Connection {
		return session.Config{}, fmt.Errorf("%s is not a connection", n.Name)
	}
	cfg, err := s.config(n)
	if err != nil {
		return cfg, err
	}
	cfg.Name = n.Name
	if cfg.Host == "" {
		return cfg, fmt.Errorf("%s has no host set", n.Name)
	}
	return cfg, nil
}

// QuickConfig builds a connection to "host" or "host:port" from the
// document defaults, without saving anything.
func (s *Service) QuickConfig(target string) (session.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	target = strings.TrimSpace(target)
	if target == "" {
		return session.Config{}, errors.New("enter a host to connect to")
	}
	cfg, err := s.config(s.doc.Root)
	if err != nil {
		return cfg, err
	}
	cfg.Name, cfg.Host = target, target
	if host, port, err := net.SplitHostPort(target); err == nil {
		if p, err := strconv.Atoi(port); err == nil {
			cfg.Host, cfg.Port = host, p
		}
	}
	return cfg, nil
}

// FindByName returns the ID of the connection with this name, or "".
func (s *Service) FindByName(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var id string
	s.doc.Root.Walk(func(n *model.Node) {
		if id == "" && n.Type == model.Connection && strings.EqualFold(n.Name, strings.TrimSpace(name)) {
			id = n.ID
		}
	})
	return id
}

func (s *Service) config(n *model.Node) (session.Config, error) {
	r := model.ResolveAll(n)
	str := func(k string) string { v, _ := r[k].(string); return v }
	flag := func(k string) bool { v, _ := r[k].(bool); return v }
	num := func(k string) int { v, _ := r[k].(int); return v }

	cfg := session.Config{
		Host:          str(model.KeyHost),
		Port:          num("rdp.port"),
		Username:      str(model.KeyUsername),
		Domain:        str(model.KeyDomain),
		AutoResize:    flag("rdp.autoResize"),
		Console:       flag("rdp.console"),
		AutoReconnect: flag("rdp.autoReconnect"),
		NLA:           flag("rdp.nla"),
		AuthLevel:     str("rdp.authLevel"),
		Gateway: session.Gateway{
			Usage:           str("rdp.gateway.usage"),
			Host:            str("rdp.gateway.host"),
			SameCredentials: str("rdp.gateway.credentials") == "same",
			Username:        str("rdp.gateway.username"),
			Domain:          str("rdp.gateway.domain"),
		},
		RedirectClipboard: flag("rdp.redirect.clipboard"),
		RedirectDrives:    flag("rdp.redirect.drives"),
		RedirectPrinters:  flag("rdp.redirect.printers"),
		Sound:             str("rdp.redirect.sound"),
		Speed:             str("rdp.speed"),
	}
	cfg.ColorDepth, _ = strconv.Atoi(str("rdp.colorDepth"))

	switch res := str("rdp.resolution"); res {
	case "fit":
	case "fullscreen":
		cfg.Fullscreen = true
	default:
		if w, h, ok := strings.Cut(res, "x"); ok {
			cfg.Width, _ = strconv.Atoi(w)
			cfg.Height, _ = strconv.Atoi(h)
		}
	}

	var err error
	if cfg.Password, err = s.secret(n, model.KeyPassword); err != nil {
		return cfg, err
	}
	if cfg.Gateway.Usage != "never" && !cfg.Gateway.SameCredentials {
		if cfg.Gateway.Password, err = s.secret(n, model.KeyGatewayPassword); err != nil {
			return cfg, err
		}
	}
	return cfg, nil
}
