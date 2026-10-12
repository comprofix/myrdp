// Package model defines the connection tree and its on-disk JSON shape.
//
// Settings are inherited by omission: a node stores only the values it
// overrides, and everything else resolves from its ancestors, then the
// document defaults, then the built-in defaults in the schema.
package model

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// SchemaVersion is the version of the connections file this build writes.
const SchemaVersion = 1

// RootID identifies the invisible root node. Its settings are the
// document-wide defaults and its children are the top level of the tree.
const RootID = "root"

type NodeType string

const (
	Folder     NodeType = "folder"
	Connection NodeType = "connection"
)

// Node is a folder or connection in the tree.
type Node struct {
	ID       string
	Type     NodeType
	Name     string
	Modified time.Time
	Revision int
	// Settings holds only this node's overrides, keyed by dotted path
	// (for example "rdp.gateway.host"). Keys the schema doesn't know are
	// kept so files written by newer versions survive a round trip.
	Settings map[string]any
	Children []*Node
	Parent   *Node
}

// Credential is a reusable named credential. Reserved in the file format;
// the secret itself lives in the OS keyring.
type Credential struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username,omitempty"`
	Domain   string `json:"domain,omitempty"`
}

// Document is the whole connections file.
type Document struct {
	SchemaVersion int
	Root          *Node
	Credentials   []*Credential
	// Extra holds unknown top-level keys for round-tripping.
	Extra map[string]json.RawMessage
}

// NewDocument returns an empty document.
func NewDocument() *Document {
	return &Document{SchemaVersion: SchemaVersion, Root: newRoot()}
}

func newRoot() *Node {
	return &Node{ID: RootID, Type: Folder, Name: "Defaults", Settings: map[string]any{}}
}

// NewID returns a random UUID (version 4).
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Touch records a change to the node.
func (n *Node) Touch() {
	n.Modified = time.Now().UTC().Truncate(time.Second)
	n.Revision++
}

// Index returns the position of n among its parent's children, or -1.
func (n *Node) Index() int {
	if n.Parent == nil {
		return -1
	}
	for i, c := range n.Parent.Children {
		if c == n {
			return i
		}
	}
	return -1
}

// IsDescendantOf reports whether n is other or sits somewhere below it.
func (n *Node) IsDescendantOf(other *Node) bool {
	for p := n; p != nil; p = p.Parent {
		if p == other {
			return true
		}
	}
	return false
}

// Walk calls fn for n and every node below it.
func (n *Node) Walk(fn func(*Node)) {
	fn(n)
	for _, c := range n.Children {
		c.Walk(fn)
	}
}

// Lookup finds the nearest value for key, starting at n and walking up to
// the root. It returns the node that supplied it.
func (n *Node) Lookup(key string) (any, *Node) {
	for p := n; p != nil; p = p.Parent {
		if v, ok := p.Settings[key]; ok {
			return v, p
		}
	}
	return nil, nil
}

// Flatten turns nested objects into dotted keys.
func Flatten(nested map[string]any) map[string]any {
	flat := map[string]any{}
	var walk func(prefix string, m map[string]any)
	walk = func(prefix string, m map[string]any) {
		for k, v := range m {
			if child, ok := v.(map[string]any); ok {
				walk(prefix+k+".", child)
				continue
			}
			flat[prefix+k] = v
		}
	}
	walk("", nested)
	return flat
}

// Nest is the inverse of Flatten.
func Nest(flat map[string]any) map[string]any {
	nested := map[string]any{}
	for key, v := range flat {
		parts := strings.Split(key, ".")
		m := nested
		for _, p := range parts[:len(parts)-1] {
			child, ok := m[p].(map[string]any)
			if !ok {
				child = map[string]any{}
				m[p] = child
			}
			m = child
		}
		m[parts[len(parts)-1]] = v
	}
	return nested
}

type nodeHeader struct {
	ID       string    `json:"id"`
	Type     NodeType  `json:"type"`
	Name     string    `json:"name"`
	Modified time.Time `json:"modified"`
	Revision int       `json:"revision"`
}

// Keys of a node object that are not settings.
var reservedNodeKeys = map[string]bool{
	"id": true, "type": true, "name": true, "modified": true, "revision": true, "children": true,
}

// writeMembers appends the members of m to an open JSON object in buf.
func writeMembers(buf *bytes.Buffer, m map[string]any) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		kb, _ := json.Marshal(k)
		vb, err := json.Marshal(m[k])
		if err != nil {
			return err
		}
		buf.WriteByte(',')
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(vb)
	}
	return nil
}

// MarshalJSON writes the identity fields first, then the node's overrides
// as nested objects, then its children.
func (n *Node) MarshalJSON() ([]byte, error) {
	head, err := json.Marshal(nodeHeader{n.ID, n.Type, n.Name, n.Modified, n.Revision})
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.Write(head[:len(head)-1])
	if err := writeMembers(&buf, Nest(n.Settings)); err != nil {
		return nil, err
	}
	if len(n.Children) > 0 {
		cb, err := json.Marshal(n.Children)
		if err != nil {
			return nil, err
		}
		buf.WriteString(`,"children":`)
		buf.Write(cb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func (n *Node) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var head nodeHeader
	if err := json.Unmarshal(data, &head); err != nil {
		return err
	}
	n.ID, n.Type, n.Name, n.Modified, n.Revision = head.ID, head.Type, head.Name, head.Modified, head.Revision
	if n.ID == "" {
		n.ID = NewID()
	}
	if n.Type != Folder && n.Type != Connection {
		return fmt.Errorf("node %q: unknown type %q", n.Name, n.Type)
	}
	if c, ok := raw["children"]; ok {
		if err := json.Unmarshal(c, &n.Children); err != nil {
			return err
		}
	}
	nested := map[string]any{}
	for k, v := range raw {
		if reservedNodeKeys[k] {
			continue
		}
		var value any
		if err := json.Unmarshal(v, &value); err != nil {
			return err
		}
		nested[k] = value
	}
	n.Settings = Flatten(nested)
	return nil
}

var reservedDocKeys = map[string]bool{
	"schemaVersion": true, "defaults": true, "credentials": true, "tree": true,
}

func (d *Document) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, `{"schemaVersion":%d`, d.SchemaVersion)
	parts := []struct {
		key   string
		value any
	}{
		{"defaults", Nest(d.Root.Settings)},
		{"credentials", d.Credentials},
		{"tree", d.Root.Children},
	}
	for _, p := range parts {
		b, err := json.Marshal(p.value)
		if err != nil {
			return nil, err
		}
		if string(b) == "null" {
			b = []byte("[]")
		}
		fmt.Fprintf(&buf, `,%q:%s`, p.key, b)
	}
	extra := map[string]any{}
	for k, v := range d.Extra {
		extra[k] = v
	}
	if err := writeMembers(&buf, extra); err != nil {
		return nil, err
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func (d *Document) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var known struct {
		SchemaVersion int            `json:"schemaVersion"`
		Defaults      map[string]any `json:"defaults"`
		Credentials   []*Credential  `json:"credentials"`
		Tree          []*Node        `json:"tree"`
	}
	if err := json.Unmarshal(data, &known); err != nil {
		return err
	}
	d.SchemaVersion = known.SchemaVersion
	d.Credentials = known.Credentials
	d.Root = newRoot()
	d.Root.Settings = Flatten(known.Defaults)
	d.Root.Children = known.Tree
	d.Root.Walk(func(n *Node) {
		for _, c := range n.Children {
			c.Parent = n
		}
	})
	d.Extra = map[string]json.RawMessage{}
	for k, v := range raw {
		if !reservedDocKeys[k] {
			d.Extra[k] = v
		}
	}
	return nil
}
