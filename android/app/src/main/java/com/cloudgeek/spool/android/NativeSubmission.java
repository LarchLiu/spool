package com.cloudgeek.spool.android;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.UUID;

/** Immutable submitted bytes, with selection adoption and one deferred stop for the server ID. */
final class NativeSubmission<T> {
    final int session;
    final String message, turnID = "pending-" + UUID.randomUUID();
    final List<T> files;
    String key, chatID;
    boolean stopRequested;

    NativeSubmission(int session, String key, String chatID, String message, List<T> files) {
        this.session = session; this.key = key; this.chatID = chatID;
        this.message = message; this.files = Collections.unmodifiableList(new ArrayList<>(files));
    }

    boolean current(int session, String key) { return this.session == session && this.key.equals(key); }
    void adopt(String id) { chatID = id; key = id; }
    boolean requestStop() { if (stopRequested) return false; stopRequested = true; return true; }
}
