package com.cloudgeek.spool.android;

import java.net.URI;
import java.util.Locale;

/** One HTTPS origin, including its port; never a suffix or substring match. */
final class OriginPolicy {
    final String origin;
    final String host;
    final int port;

    OriginPolicy(String address) {
        URI uri = URI.create(address.trim());
        host = uri.getHost() == null ? "" : uri.getHost().toLowerCase(Locale.ROOT);
        port = uri.getPort() == -1 ? 443 : uri.getPort();
        if (!"https".equals(uri.getScheme()) || uri.getRawUserInfo() != null
                || uri.getRawQuery() != null || uri.getRawFragment() != null
                || !(uri.getRawPath() == null || uri.getRawPath().isEmpty() || "/".equals(uri.getRawPath()))
                || !host.matches("[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?")
                || !host.contains(".") || host.contains("..") || host.endsWith(".localhost")
                || host.matches("[0-9.]+") || port < 1 || port > 65535) {
            throw new IllegalArgumentException("Use an HTTPS DNS origin, for example https://spool.example.ts.net");
        }
        origin = "https://" + host + (port == 443 ? "" : ":" + port);
    }

    boolean matches(String address) {
        try {
            URI uri = URI.create(address);
            return "https".equals(uri.getScheme()) && uri.getRawUserInfo() == null
                    && host.equalsIgnoreCase(uri.getHost())
                    && (uri.getPort() == -1 ? 443 : uri.getPort()) == port;
        } catch (IllegalArgumentException e) {
            return false;
        }
    }

    boolean allowsResource(String address) {
        if (address.startsWith("data:")) return true;
        if (address.startsWith("blob:")) return matches(address.substring(5));
        return matches(address);
    }

    static boolean isLoginURL(String address) {
        try {
            URI uri = URI.create(address);
            return "https".equals(uri.getScheme()) && "login.tailscale.com".equals(uri.getHost())
                    && uri.getRawUserInfo() == null && (uri.getPort() == -1 || uri.getPort() == 443);
        } catch (IllegalArgumentException e) {
            return false;
        }
    }

    static boolean isExternalWebURL(String address) {
        try {
            URI uri = URI.create(address);
            return ("https".equals(uri.getScheme()) || "http".equals(uri.getScheme()))
                    && uri.getHost() != null && uri.getRawUserInfo() == null;
        } catch (IllegalArgumentException e) {
            return false;
        }
    }
}
