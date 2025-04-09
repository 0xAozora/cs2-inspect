package types

import (
	"time"
)

//go:generate msgp

type Lookup struct {
	S uint64 `msg:"s,omitempty" json:"s,omitzero"`
	A uint64 `msg:"a" json:"a"`
	D uint64 `msg:"d" json:"d"`
	M uint64 `msg:"m,omitempty" json:"m,omitzero"`

	_ float32
	_ uint16
	_ []Sticker
	_ *Keychain

	_ time.Time
}

type Request_Safe struct {
	L []*Lookup `msg:"l" json:"l"`
	S uint64    `msg:"s,omitempty" json:"s,omitzero"` // For inspecting whole inventory
}

type Request struct {
	L []*Info `msg:"l"`
	S uint64  `msg:"s,omitempty" json:"s,omitzero"`
}

type Info struct {
	S uint64 `msg:"s,omitempty" json:"s,omitzero"`
	A uint64 `msg:"a" json:"a"`
	D uint64 `msg:"d" json:"d"`
	M uint64 `msg:"m,omitempty" json:"m,omitzero"`

	Float    float32   `msg:"f" json:"f"`
	Seed     uint16    `msg:"e" json:"e"`
	Stickers []Sticker `msg:"t,omitempty" json:"t,omitempty"`
	Keychain *Keychain `msg:"k,omitempty" json:"k,omitempty"`

	Time time.Time `msg:"-" json:"-"`
}

type Response struct {
	Info []*Info `msg:"i"`
}
