package com.cloudgeek.spool.android;

import java.net.URI;
import java.net.URLDecoder;
import java.nio.charset.StandardCharsets;
import java.util.Locale;
import java.io.ByteArrayOutputStream;
import java.nio.ByteBuffer;

/** User-initiated external app links only; never an API/transport origin policy. */
final class NativeExternalLink {
    static boolean allowed(String address) {
        if (address == null || address.isEmpty()) return false;
        try {
            String decoded = decode(address);
            for (int i = 0; i < decoded.length(); i++) if (decoded.charAt(i) < 32 || decoded.charAt(i) == 127) return false;
            URI uri = new URI(address);
            String scheme = uri.getScheme() == null ? "" : uri.getScheme().toLowerCase(Locale.ROOT);
            if (scheme.equals("http") || scheme.equals("https")) {
                // URI.getHost rejects Unicode/underscore DNS labels that browsers support.
                String authority = uri.getRawAuthority();
                if (authority == null || authority.isEmpty() || uri.getRawUserInfo() != null) return false;
                String host = URLDecoder.decode(authority, StandardCharsets.UTF_8.name());
                return !host.contains("@") && !host.contains("\\") && !host.matches("(?s).*[\\s/?#].*");
            }
            return (scheme.equals("mailto") || scheme.equals("irc") || scheme.equals("ircs") || scheme.equals("xmpp"))
                    && uri.getRawSchemeSpecificPart() != null && !uri.getRawSchemeSpecificPart().isEmpty();
        } catch (Exception invalid) { return false; }
    }

    static boolean inline(String target) {
        return target != null && (target.startsWith("#") || NativeFileTarget.parse(target) != null || allowed(target));
    }

    private static String decode(String address) throws Exception {
        StringBuilder value = new StringBuilder();
        for (int i = 0; i < address.length();) {
            if (address.charAt(i) != '%') { value.append(address.charAt(i++)); continue; }
            ByteArrayOutputStream bytes = new ByteArrayOutputStream();
            while (i < address.length() && address.charAt(i) == '%') {
                if (i + 2 >= address.length()) throw new IllegalArgumentException("Incomplete URL escape");
                int high = Character.digit(address.charAt(i + 1), 16), low = Character.digit(address.charAt(i + 2), 16);
                if (high < 0 || low < 0) throw new IllegalArgumentException("Invalid URL escape");
                bytes.write(high * 16 + low); i += 3;
            }
            value.append(StandardCharsets.UTF_8.newDecoder().decode(ByteBuffer.wrap(bytes.toByteArray())));
        }
        return value.toString();
    }
}
