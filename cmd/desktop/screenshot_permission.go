package main

import (
	"context"
	"errors"

	"github.com/egoist/mygo/plugins/permissionflow"
)

const screenshotPermissionMessage = "Screen recording permission is required. Allow Spool in System Settings, then restart Spool and try again."

func desktopScreenshotPermission(ctx context.Context) error {
	return ensureScreenshotPermission(ctx,
		func() (permissionflow.Status, error) { return permissionflow.Check(permissionflow.ScreenRecording) },
		func(ctx context.Context) (permissionflow.Status, error) {
			return permissionflow.Request(ctx, permissionflow.ScreenRecording)
		},
		func(ctx context.Context) (permissionflow.Status, error) {
			return permissionflow.Guide(ctx, permissionflow.ScreenRecording, permissionflow.GuideOptions{})
		},
	)
}

func ensureScreenshotPermission(ctx context.Context, check func() (permissionflow.Status, error), request, guide func(context.Context) (permissionflow.Status, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	status, err := check()
	if err == nil && status == permissionflow.Granted {
		return nil
	}
	status, err = request(ctx)
	if err == nil && status == permissionflow.Granted {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	status, err = guide(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil && status == permissionflow.Granted {
		return nil
	}
	return errors.New(screenshotPermissionMessage)
}
