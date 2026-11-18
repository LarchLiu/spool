package com.cloudgeek.spool.android;

import android.widget.ImageView;
import java.util.function.Consumer;

/** One source identity for thumbnails, full previews and downloads. */
final class NativeImageResource {
    final String name, source, id;
    final NativeFileData local;
    private NativeImageResource(String name, String source, String id, NativeFileData local) {
        this.name = name; this.source = source; this.id = id; this.local = local;
    }

    static NativeImageResource source(String name, String source) {
        if (!NativeFileData.inlineImage(source) && !NativeFileData.allowedPath(source)) return null;
        return new NativeImageResource(name, source, "", null);
    }

    static NativeImageResource local(String name, String id, NativeFileData data) {
        return new NativeImageResource(name, "", id, data);
    }

    static NativeImageResource transcript(String project, String chat, String path, String source, String name) {
        if (NativeFileData.inlineImage(source)) return source(name, source);
        if (NativeFileData.allowedPath(source) && source.startsWith("/api/chats/" + android.net.Uri.encode(chat) + "/")) return source(name, source);
        NativeFileTarget target = NativeFileTarget.parse(path);
        if (target == null || project.isEmpty() || chat.isEmpty()) return null;
        return source(name, NativeFileData.filePath(project, chat, target.path, "image"));
    }

    void load(NativeImages images, ImageView image, int edge, Consumer<Exception> completion) {
        load(images, image, "", edge, completion);
    }

    void load(NativeImages images, ImageView image, String scope, int edge, Consumer<Exception> completion) {
        if (local != null) images.local(image, id, local, edge, completion);
        else if (NativeFileData.inlineImage(source)) images.inline(image, source, edge, completion);
        else images.remote(image, source, scope, edge, completion);
    }

    NativeFileData read(NativeImages.Source transport) throws Exception {
        return local != null ? local : NativeFileData.inlineImage(source) ? NativeFileData.inline(source) : transport.load(source);
    }
}
