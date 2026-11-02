package spoolnet

import "spool/internal/avatar"

var localAvatars = avatar.NewRenderer()

// RenderAvatar generates an SVG entirely on the device, without a Client,
// session, tsnet node or HTTP request. The bounded renderer reuses compiled
// styles and SVGs across display sizes, using the same definitions as Web.
func RenderAvatar(style, seed string) (string, error) {
	image, err := localAvatars.Render(style, seed)
	if err != nil {
		return "", err
	}
	return image.SVG, nil
}
