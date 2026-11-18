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
        if (response.optInt("status") != 200) throw new NativeImageFailure(response.optInt("status"));
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

    static String imageAction(String project, String chat, JSONObject file, String mode, String workspace) {
        String address = file.optString(mode.equals("download") ? "download_url" : "preview_url");
        if (allowedPath(address) && address.startsWith(apiBase(project, chat) + "/")) return address;
        return filePath(project, chat, file.optString("path"), mode, workspace);
    }

    static boolean allowedPath(String path) {
        if (path == null) return false;
        if (toolImagePath(path)) return true;
        if (!path.matches("^/api/(?:projects|chats)/[^/?#]+/(?:file|attachments)\\?[^#]+$")) return false;
        try {
            Uri uri = Uri.parse(path);
            java.util.Set<String> keys = uri.getQueryParameterNames();
            for (String key : keys) if (uri.getQueryParameters(key).size() != 1) return false;
            if (uri.getPath().endsWith("/attachments"))
                return keys.equals(java.util.Set.of("id")) && !uri.getQueryParameter("id").isEmpty();
            if (!keys.contains("path") || uri.getQueryParameter("path").isEmpty()) return false;
            if (!java.util.Set.of("path", "image", "download", "workspace").containsAll(keys)) return false;
            for (String key : new String[]{"image", "download"}) if (keys.contains(key) && !"1".equals(uri.getQueryParameter(key))) return false;
            return true;
        } catch (RuntimeException invalid) { return false; }
    }

    static boolean toolImagePath(String path) {
        return path != null && path.matches("^/api/chats/[^/?#]+/tool-image\\?ref=(?:[a-f0-9]{64}|native-tool-[A-Za-z0-9_-]{1,16384})&index=\\d+$");
    }

    static boolean inlineImage(String source) {
        if (source == null || source.length() > ((MAX_BYTES + 2L) / 3) * 4 + 64) return false;
        int comma = source.indexOf(',');
        return comma > 0 && source.substring(0, comma).matches("data:image/(?:png|jpeg|gif|webp);base64");
    }

    static NativeFileData inline(String source) throws IOException {
        if (!inlineImage(source)) throw new NativeImageFailure(400);
        int comma = source.indexOf(',');
        try { return new NativeFileData(source.substring(5, comma - 7), java.util.Base64.getDecoder().decode(source.substring(comma + 1))); }
        catch (IllegalArgumentException error) { throw new NativeImageFailure(400); }
    }

    static boolean image(String mediaType) { return mediaType != null && mediaType.startsWith("image/"); }

    static String imageType(String name) {
        String extension = name.substring(name.lastIndexOf('.') + 1).toLowerCase(Locale.ROOT);
        switch (extension) {
            case "jpg": case "jpeg": return "image/jpeg";
            case "png": case "gif": case "webp": return "image/" + extension;
            default: return "";
        }
    }

    static boolean code(String name) { return name.toLowerCase(Locale.ROOT).matches(".*\\.(json|go|rs|ts|tsx|js|py|yaml|yml|toml)$"); }

    static String excerpt(String name, String mediaType, byte[] bytes) {
        if (!mediaType.matches("^(text/.*|application/(json|javascript|xml).*)$")
                && !name.toLowerCase(Locale.ROOT).matches(".*\\.(json|md|txt|csv|go|rs|ts|tsx|js|py|yaml|yml|toml|log)$")) return "";
        return new String(bytes, 0, Math.min(4096, bytes.length), StandardCharsets.UTF_8).replaceFirst("^\ufeff", "");
    }
}
