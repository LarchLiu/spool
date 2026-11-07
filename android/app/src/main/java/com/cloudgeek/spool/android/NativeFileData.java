package com.cloudgeek.spool.android;

import android.net.Uri;
import android.util.Base64;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.util.Locale;
import org.json.JSONObject;

/** Bounded file payloads cross the existing Go boundary; never resolve an arbitrary image URL. */
final class NativeFileData {
    static final int MAX_BYTES = 16 * 1024 * 1024;
    final String mediaType;
    final byte[] bytes;

    NativeFileData(String mediaType, byte[] bytes) throws IOException {
        if (bytes == null || bytes.length > MAX_BYTES) throw new IOException("File exceeded its size budget");
        String normalized = mediaType == null ? "" : mediaType.split(";", 2)[0].trim().toLowerCase(Locale.ROOT);
        this.mediaType = normalized.matches("[a-z0-9!#$&^_.+-]+/[a-z0-9!#$&^_.+-]+") ? normalized : "application/octet-stream";
        this.bytes = bytes;
    }

    static NativeFileData response(JSONObject response) throws IOException {
        if (response.optInt("status") != 200) throw new IOException("File unavailable (" + response.optInt("status") + ")");
        String body = response.optString("body");
        boolean base64 = response.optBoolean("base64");
        if (body.length() > (base64 ? ((MAX_BYTES + 2L) / 3) * 4 + 8 : MAX_BYTES)) throw new IOException("File exceeded its size budget");
        try {
            return new NativeFileData(response.optString("mediaType"), base64 ? Base64.decode(body, Base64.DEFAULT) : body.getBytes(StandardCharsets.UTF_8));
        } catch (IllegalArgumentException error) { throw new IOException("Invalid file encoding", error); }
    }

    static String attachmentPath(String chatID, String fileID) {
        if (chatID.isEmpty() || fileID.isEmpty()) throw new IllegalArgumentException("Missing attachment identity");
        return "/api/chats/" + Uri.encode(chatID) + "/attachments?id=" + Uri.encode(fileID);
    }

    static String projectPath(String project, String path, String mode) {
        return filePath(project, "", path, mode);
    }

    static String apiBase(String project, String chat) {
        return chat.isEmpty() ? "/api/projects/" + Uri.encode(project) : "/api/chats/" + Uri.encode(chat);
    }

    static String filePath(String project, String chat, String path, String mode) {
        return filePath(project, chat, path, mode, "");
    }

    static String filePath(String project, String chat, String path, String mode, String workspace) {
        if (project.isEmpty() || path.isEmpty() || !(mode.isEmpty() || mode.equals("download") || mode.equals("image"))) throw new IllegalArgumentException("Invalid project file target");
        return apiBase(project, chat) + "/file?path=" + Uri.encode(path) + (mode.isEmpty() ? "" : "&" + mode + "=1")
                + (chat.isEmpty() || workspace.isEmpty() ? "" : "&workspace=" + Uri.encode(workspace));
    }

    static boolean allowedPath(String path) {
        return path != null && (path.matches("^/api/chats/[^/?#]+/attachments\\?id=[^&#]+$")
                || path.matches("^/api/chats/[^/?#]+/tool-image\\?ref=[a-f0-9]{64}&index=\\d+$")
                || path.matches("^/api/(?:projects|chats)/[^/?#]+/file\\?path=[^&#]+(?:&(?:image|download)=1)?(?:&workspace=[^&#]+)?$"));
    }

    static boolean inlineImage(String source) {
        if (source == null || source.length() > ((MAX_BYTES + 2L) / 3) * 4 + 64) return false;
        int comma = source.indexOf(',');
        return comma > 0 && source.substring(0, comma).matches("data:image/(?:png|jpeg|gif|webp);base64");
    }

    static NativeFileData inline(String source) throws IOException {
        if (!inlineImage(source)) throw new IOException("Invalid inline image");
        int comma = source.indexOf(',');
        try { return new NativeFileData(source.substring(5, comma - 7), java.util.Base64.getDecoder().decode(source.substring(comma + 1))); }
        catch (IllegalArgumentException error) { throw new IOException("Invalid image encoding", error); }
    }

    static boolean image(String mediaType) { return mediaType != null && mediaType.startsWith("image/"); }

    static boolean code(String name) { return name.toLowerCase(Locale.ROOT).matches(".*\\.(json|go|rs|ts|tsx|js|py|yaml|yml|toml)$"); }

    static String excerpt(String name, String mediaType, byte[] bytes) {
        if (!mediaType.matches("^(text/.*|application/(json|javascript|xml).*)$")
                && !name.toLowerCase(Locale.ROOT).matches(".*\\.(json|md|txt|csv|go|rs|ts|tsx|js|py|yaml|yml|toml|log)$")) return "";
        return new String(bytes, 0, Math.min(4096, bytes.length), StandardCharsets.UTF_8).replaceFirst("^\ufeff", "");
    }
}
