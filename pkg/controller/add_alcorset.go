package controller

import (
	"github.com/onionpiece/alcorset/pkg/controller/alcorset"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerFuncs = append(AddToManagerFuncs, alcorset.Add)
}
