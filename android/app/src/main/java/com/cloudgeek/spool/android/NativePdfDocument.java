package com.cloudgeek.spool.android;

import android.graphics.Bitmap;
import android.graphics.Color;
import android.graphics.pdf.PdfRenderer;
import android.os.ParcelFileDescriptor;
import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;

/** Seekable native PDF input, bounded page bitmaps, and deterministic descriptor ownership. */
abstract class NativePdfDocument implements AutoCloseable {
    static final int MAX_EDGE = 4096;
    static final int MAX_PIXELS = 4_000_000;
    static final class Size {
        final int width, height;
        Size(int width, int height) { this.width = width; this.height = height; }
    }
    static final class Page {
        final Bitmap bitmap;
        final int width, height;
        Page(Bitmap bitmap, int width, int height) { this.bitmap = bitmap; this.width = width; this.height = height; }
    }
    abstract int pageCount() throws IOException;
    abstract Size pageSize(int index) throws IOException;
    abstract Page render(int index, int requestedWidth) throws IOException;
    @Override public abstract void close();

    static NativePdfDocument open(File cache, NativeFileData file) throws IOException {
        byte[] data = file.bytes;
        if (data.length < 5 || data[0] != '%' || data[1] != 'P' || data[2] != 'D' || data[3] != 'F' || data[4] != '-')
            throw new IOException("Invalid PDF signature");
        File temporary = File.createTempFile("spool-pdf-", ".pdf", cache);
        ParcelFileDescriptor descriptor = null;
        PdfRenderer renderer = null;
        try {
            try (FileOutputStream stream = new FileOutputStream(temporary)) { stream.write(data); }
            descriptor = ParcelFileDescriptor.open(temporary, ParcelFileDescriptor.MODE_READ_ONLY);
            renderer = new PdfRenderer(descriptor);
            descriptor = null; // PdfRenderer owns the descriptor after successful construction.
            return new SystemDocument(renderer);
        } finally {
            try { if (descriptor != null) descriptor.close(); }
            finally {
                // Android permits unlinking an open file: only the renderer keeps its seekable bytes.
                if (!temporary.delete() && temporary.exists()) {
                    if (renderer != null) renderer.close();
                    throw new IOException("Could not remove PDF scratch file");
                }
            }
        }
    }

    private static final class SystemDocument extends NativePdfDocument {
        private final PdfRenderer renderer;
        private boolean closed;
        SystemDocument(PdfRenderer renderer) { this.renderer = renderer; }

        @Override synchronized int pageCount() throws IOException {
            if (closed) throw new IOException("PDF closed");
            return renderer.getPageCount();
        }

        @Override synchronized Size pageSize(int index) throws IOException {
            if (closed) throw new IOException("PDF closed");
            try (PdfRenderer.Page page = renderer.openPage(index)) {
                return new Size(page.getWidth(), page.getHeight());
            }
        }

        @Override synchronized Page render(int index, int requestedWidth) throws IOException {
            if (closed) throw new IOException("PDF closed");
            try (PdfRenderer.Page page = renderer.openPage(index)) {
                int width = page.getWidth(), height = page.getHeight();
                if (width <= 0 || height <= 0) throw new IOException("Invalid PDF page size");
                double scale = Math.min(Math.max(1, requestedWidth) / (double) width,
                        Math.min(MAX_EDGE / (double) Math.max(width, height), Math.sqrt(MAX_PIXELS / ((double) width * height))));
                int pixelsWide = Math.max(1, (int) Math.floor(width * scale));
                int pixelsHigh = Math.max(1, (int) Math.floor(height * scale));
                Bitmap bitmap = Bitmap.createBitmap(pixelsWide, pixelsHigh, Bitmap.Config.ARGB_8888);
                bitmap.eraseColor(Color.WHITE);
                try { page.render(bitmap, null, null, PdfRenderer.Page.RENDER_MODE_FOR_DISPLAY); }
                catch (RuntimeException error) { bitmap.recycle(); throw error; }
                return new Page(bitmap, width, height);
            }
        }

        @Override public synchronized void close() {
            if (closed) return;
            closed = true;
            renderer.close();
        }
    }
}
