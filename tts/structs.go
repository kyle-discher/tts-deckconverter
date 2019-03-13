package tts

import (
	"errors"
	"math"
	"strconv"
	"strings"
)

type Decimal float64

func (d Decimal) MarshalJSON() ([]byte, error) {
	f := float64(d)

	if math.IsInf(f, 0) || math.IsNaN(f) {
		return nil, errors.New("unsupported value")
	}

	str := strconv.FormatFloat(f, 'f', -1, 32)

	if !strings.Contains(str, ".") {
		// Add a trailing 0 if it's not a decimal number
		str += ".0"
	}

	return []byte(str), nil
}

// UnmarshalJSON will unmarshal a JSON value into
// the propert representation of that value.
func (d *Decimal) UnmarshalJSON(text []byte) error {
	t := string(text)
	if t == "null" {
		return nil
	}
	i, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return err
	}
	*d = Decimal(i)
	return nil
}

type ObjectType string

const (
	DeckObject       ObjectType = "Deck"
	DeckCustomObject            = "DeckCustom"
	CardObject                  = "Card"
)

var DefaultTransform Transform = Transform{
	PosX:   0,
	PosY:   0,
	PosZ:   0,
	RotX:   0,
	RotY:   180,
	RotZ:   180,
	ScaleX: 1,
	ScaleY: 1,
	ScaleZ: 1,
}

var DefaultColorDiffuse ColorDiffuse = ColorDiffuse{
	Red:   0.713239133,
	Green: 0.713239133,
	Blue:  0.713239133,
}

type SavedObject struct {
	SaveName       string   `json:"SaveName"`
	GameMode       string   `json:"GameMode"`
	Gravity        Decimal  `json:"Gravity"`
	PlayArea       Decimal  `json:"PlayArea"`
	Date           string   `json:"Date"`
	Table          string   `json:"Table"`
	Sky            string   `json:"Sky"`
	Note           string   `json:"Note"`
	Rules          string   `json:"Rules"`
	PlayerTurn     string   `json:"PlayerTurn"`
	XMLUI          string   `json:"XmlUI"`
	LuaScript      string   `json:"LuaScript"`
	LuaScriptState string   `json:"LuaScriptState"`
	ObjectStates   []Object `json:"ObjectStates"`
	TabStates      struct{} `json:"TabStates"`
	VersionNumber  string   `json:"VersionNumber"`
}

type Object struct {
	ObjectType   ObjectType   `json:"Name"`
	Transform    Transform    `json:"Transform"`
	Nickname     string       `json:"Nickname"`
	Description  string       `json:"Description"`
	ColorDiffuse ColorDiffuse `json:"ColorDiffuse"`
	// Locked, when set, freezes an object in place, stopping all physical
	// interactions
	Locked bool `json:"Locked"`
	// Grid makes the object snap to any grid point
	Grid bool `json:"Grid"`
	// Snap makes the object snap to any snap point
	Snap bool `json:"Snap"`
	// IgnoreFoW makes the object visible even inside fog of war
	IgnoreFoW bool `json:"IgnoreFoW"`
	// Autoraise makes the object automatically raise above potential collisions
	Autoraise bool `json:"Autoraise"`
	// Sticky makes the objects above this one attached to it when it is picked
	// up
	Sticky bool `json:"Sticky"`
	// Show a tooltip when hovering over the object (name, description, icon)
	Tooltip bool `json:"Tooltip"`
	// Should this object receive grid lines projected onto it?
	GridProjection bool `json:"GridProjection"`
	// When object is face down, it will be hidden as a question mark
	HideWhenFaceDown bool `json:"HideWhenFaceDown"`
	// Should this object go into the players' hand?
	Hands            bool                  `json:"Hands"`
	CardID           int                   `json:"CardID,omitempty"`
	SidewaysCard     bool                  `json:"SidewaysCard"`
	DeckIDs          []int                 `json:"DeckIDs,omitempty"`
	CustomDeck       map[string]CustomDeck `json:"CustomDeck,omitempty"`
	ContainedObjects []Object              `json:"ContainedObjects,omitempty"`
	States           map[string]Object     `json:"States,omitempty"`
}

type Transform struct {
	PosX   Decimal `json:"posX"`
	PosY   Decimal `json:"posY"`
	PosZ   Decimal `json:"posZ"`
	RotX   Decimal `json:"rotX"`
	RotY   Decimal `json:"rotY"`
	RotZ   Decimal `json:"rotZ"`
	ScaleX Decimal `json:"scaleX"`
	ScaleY Decimal `json:"scaleY"`
	ScaleZ Decimal `json:"scaleZ"`
}

type ColorDiffuse struct {
	Red   Decimal `json:"r"`
	Green Decimal `json:"g"`
	Blue  Decimal `json:"b"`
}

type CustomDeck struct {
	// FaceURL is the address of the card faces
	FaceURL string `json:"FaceURL"`
	// BackURL is the address of the card back (backs if UniqueBack is true)
	BackURL string `json:"BackURL"`
	// NumWidth is the number of cards in a single row of the face image
	// (and back image if UniqueBack is true)
	NumWidth int `json:"NumWidth"`
	// NumHeight is the number of cards in a single column of the face image
	// (and back image if UniqueBack is true)
	NumHeight int `json:"NumHeight"`
	// BackIsHidden determines if the BackURL should be used as the back of the
	// cards instead of the last image of the card face image
	BackIsHidden bool `json:"BackIsHidden"`
	// UniqueBack should be true if each card is using a different back
	UniqueBack bool `json:"UniqueBack"`
}

func createSavedObject(objectStates []Object) SavedObject {
	return SavedObject{
		Gravity:      0.5,
		PlayArea:     0.5,
		ObjectStates: objectStates,
	}
}

func createDefaultDeck() SavedObject {
	return createSavedObject([]Object{
		Object{
			// TODO: Find the difference between "Deck" and "DeckCustom"
			// The Scryfall mod uses "Deck" while Decker uses "DeckCustom"
			// ObjectType:       DeckCustomObject,
			ObjectType:       DeckObject,
			Transform:        DefaultTransform,
			ColorDiffuse:     DefaultColorDiffuse,
			Locked:           false,
			Grid:             true,
			Snap:             true,
			IgnoreFoW:        false,
			Autoraise:        true,
			Sticky:           true,
			Tooltip:          true,
			GridProjection:   false,
			HideWhenFaceDown: true,
			Hands:            false,
			SidewaysCard:     false,
			CustomDeck:       make(map[string]CustomDeck),
		},
	})
}
