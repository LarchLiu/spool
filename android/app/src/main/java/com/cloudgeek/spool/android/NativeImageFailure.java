package com.cloudgeek.spool.android;

import java.io.IOException;

/** Preserve transport failures separately from unsupported/invalid raster data. */
final class NativeImageFailure extends IOException {
    final int status;
    NativeImageFailure(int status) { super(message(status)); this.status = status; }

    static String message(int status) {
        if (status == 404 || status == 410) return "File not found";
        if (status == 401 || status == 403) return "Image access denied";
        if (status >= 400 && status < 500) return "This file cannot be previewed as an image";
        return "Image could not be loaded. Try again.";
    }

    static String message(Exception error) {
        return message(error instanceof NativeImageFailure ? ((NativeImageFailure) error).status : 0);
    }
}
