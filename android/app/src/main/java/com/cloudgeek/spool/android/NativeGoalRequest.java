package com.cloudgeek.spool.android;

/** Captured goal-control selection; a delayed reply cannot replace a newer chat or draft. */
final class NativeGoalRequest {
    private final int session, selection;
    private final String key, message;
    NativeGoalRequest(int session, int selection, String key, String message) {
        this.session = session; this.selection = selection; this.key = key; this.message = message;
    }
    boolean current(int session, int selection, String key) { return this.session == session && this.selection == selection && this.key.equals(key); }
    boolean clears(String text) { return message.equals(text); }
}
