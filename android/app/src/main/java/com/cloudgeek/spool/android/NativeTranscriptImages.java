package com.cloudgeek.spool.android;

import android.widget.ImageView;
import java.util.function.Consumer;

/** Per-chat image actions supplied by the authenticated screen, including historical inline pixels. */
interface NativeTranscriptImages {
    void load(ImageView image, String path, String source, int edge, Consumer<Exception> completion);
    void preview(String path, String source, String name);
}
