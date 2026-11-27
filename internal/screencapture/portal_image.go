package screencapture

import (
	"bytes"
	"image/png"
	"io"
	"net/url"
	"os"
	"path/filepath"
)

func readPortalImage(uri string) ([]byte, error) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !filepath.IsAbs(u.Path) {
		return nil, ErrFailed
	}
	file, err := os.Open(u.Path)
	if err != nil {
		return nil, ErrFailed
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 {
		return nil, ErrFailed
	}
	if info.Size() > 64*1024*1024 {
		return nil, ErrTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(file, 64*1024*1024+1))
	if err != nil {
		return nil, ErrFailed
	}
	if len(data) > 64*1024*1024 {
		return nil, ErrTooLarge
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 100_000_000 {
		return nil, ErrFailed
	}
	return data, nil
}
