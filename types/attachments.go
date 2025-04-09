package types

//go:generate msgp

type Sticker struct {
	ID   uint32  `msg:"i" json:"i"`
	Wear float32 `msg:"f,omitempty" json:"f,omitzero"`
	Slot uint8   `msg:"s,omitempty" json:"s,omitzero"`
	X    float32 `msg:"x,omitempty" json:"x,omitzero"`
	Y    float32 `msg:"y,omitempty" json:"y,omitzero"`
	R    float32 `msg:"r,omitempty" json:"r,omitzero"`
}

type Keychain struct {
	ID      uint32  `msg:"i" json:"i"`
	Pattern uint32  `msg:"p" json:"p"`
	X       float32 `msg:"x" json:"x"`
	Y       float32 `msg:"y" json:"y"`
	Z       float32 `msg:"z" json:"z"`
}
