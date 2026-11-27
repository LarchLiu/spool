//go:build !darwin

package main

import (
	"context"
	"fmt"
)

func (p *panel) clickSmokeElement(context.Context, string) error {
	return fmt.Errorf("native fixture mouse events are only supported on macOS")
}
