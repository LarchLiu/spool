package main

import "github.com/egoist/mygo"

type directoryPicker struct {
	pending bool
}

func (p *directoryPicker) open(
	opts mygo.OpenDialogOptions,
	show func(mygo.OpenDialogOptions) ([]string, error),
	update func(func()),
	complete func([]string, error),
) {
	if p.pending {
		return
	}
	p.pending = true
	// Never wait for a native modal inside the immediate-mode UI's frame.
	go func() {
		paths, err := show(opts)
		update(func() {
			p.pending = false
			complete(paths, err)
		})
	}()
}

func (p *panel) applyProjectDirectory(paths []string, err error) {
	if err != nil {
		p.message = err.Error()
	} else if len(paths) > 0 {
		p.project.Path = paths[0]
	}
}
