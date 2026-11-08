package com.cloudgeek.spool.android;

import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.Base64;
import org.json.JSONObject;

/** Untrusted QR payload. Acceptance pins an identity; it never grants a login. */
final class RelayPairing {
    final String code, relay, id, fingerprint;

    RelayPairing(String input) {
        try {
            code = input.trim();
            if (!code.startsWith("spool-pair:") || code.length() > 8192) throw new IllegalArgumentException();
            JSONObject value = new JSONObject(new String(Base64.getUrlDecoder().decode(code.substring(11)), StandardCharsets.UTF_8));
            if (value.getInt("v") != 1) throw new IllegalArgumentException();
            byte[] key = Base64.getDecoder().decode(value.getString("key"));
            byte[] token = Base64.getUrlDecoder().decode(value.getString("token"));
            if (key.length != 32 || token.length != 32) throw new IllegalArgumentException();
            relay = value.getString("relay");
            URI uri = new URI(relay);
            if (!"https".equals(uri.getScheme()) || uri.getHost() == null || uri.getUserInfo() != null
                    || uri.getRawQuery() != null || uri.getRawFragment() != null
                    || !(uri.getPath().isEmpty() || uri.getPath().equals("/"))
                    || uri.getPort() == 0 || uri.getPort() > 65535) throw new IllegalArgumentException();
            byte[] hash = MessageDigest.getInstance("SHA-256").digest(key);
            StringBuilder hex = new StringBuilder();
            for (int i = 0; i < hash.length; i++) {
                if (i > 0 && i % 4 == 0) hex.append(' ');
                hex.append(String.format(java.util.Locale.ROOT, "%02x", hash[i] & 255));
            }
            fingerprint = hex.toString();
            String alphabet = "abcdefghijklmnopqrstuvwxyz234567";
            StringBuilder name = new StringBuilder(); int buffer = 0, bits = 0;
            for (byte b : hash) {
                buffer = (buffer << 8) | (b & 255); bits += 8;
                while (bits >= 5) { bits -= 5; name.append(alphabet.charAt((buffer >> bits) & 31)); }
            }
            if (bits > 0) name.append(alphabet.charAt((buffer << (5 - bits)) & 31));
            id = name.toString();
        } catch (Exception error) { throw new IllegalArgumentException("Invalid Spool pairing code", error); }
    }
}
