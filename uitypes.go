package logkeeper

import (
	"fmt"
)

var Colors = []string{"#333", "seagreen", "steelblue",
	"mediumpurple", "crimson", "darkkhaki",
	"darkgreen", "rosybrown", "chocolate",
	"orangered", "darkseagreen", "royalblue",
	"slategray",
}

// ColorSet is a structure to track unique logger names and assign a color
// number to each one.
type ColorSet struct {
	vals     map[string]int
	incCount int
}

type ColorDef struct {
	Name  string
	Color string
}

func NewColorSet() *ColorSet {
	return &ColorSet{map[string]int{}, 0}
}

// GetColor returns a unique color name for the given key, creating a new one
// in its internal map if it does not already exist.
func (self *ColorSet) GetColor(inputKey interface{}) (string, error) {
	if strKey, ok := inputKey.(string); ok {
		if count, ok2 := self.vals[strKey]; ok2 {
			return fmt.Sprintf("color%v", count), nil
		} else {
			self.incCount++
			self.vals[strKey] = self.incCount
			return fmt.Sprintf("color%v", self.incCount), nil
		}
	} else {
		return "", fmt.Errorf("not a string key")
	}
}

// GetAllColors returns a list of all the ColorDef entries stored internally,
// where each ColorDef is composed of a name and actual HTML color value.
func (self *ColorSet) GetAllColors() []ColorDef {
	returnVals := make([]ColorDef, 0, len(self.vals))
	index := 0
	for _, v := range self.vals {
		color := ColorDef{Name: fmt.Sprintf("color%v", v), Color: Colors[index%len(Colors)]}
		returnVals = append(returnVals, color)
		index++
	}
	return returnVals
}

type MutableVar struct {
	Value interface{}
}

func (self *MutableVar) Get() interface{} {
	return self.Value
}

func (self *MutableVar) Set(v interface{}) interface{} {
	self.Value = v
	return ""
}
