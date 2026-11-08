package com.cloudgeek.spool.android;

import android.view.View;
import android.view.ViewGroup;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashMap;
import java.util.IdentityHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.function.Supplier;

/** Reconcile flat native siblings without detaching unchanged keyed documents. */
final class NativeKeyedViews {
    private static final class Entry {
        final Object signature;
        final View view;
        Entry(Object signature, View view) { this.signature = signature; this.view = view; }
    }
    private final ViewGroup parent;
    private Map<String, Entry> current = new HashMap<>(), next = new HashMap<>();
    private final List<View> wanted = new ArrayList<>();
    // A committed but not yet attached pass; the owner attaches it when the parent becomes visible.
    private List<View> pending;

    NativeKeyedViews(ViewGroup parent) { this.parent = parent; }

    void begin() { next.clear(); wanted.clear(); pending = null; }

    View use(String key, Object value, Supplier<? extends View> create) {
        Entry entry = current.get(key);
        if (entry == null || !NativeChatState.equivalent(entry.signature, value))
            entry = new Entry(NativeChatState.snapshot(value), create.get());
        if (next.put(key, entry) != null) throw new IllegalArgumentException("Duplicate native sibling key: " + key);
        wanted.add(entry.view);
        return entry.view;
    }

    void end() {
        sync(parent, wanted);
        Map<String, Entry> old = current; current = next; next = old;
        next.clear(); wanted.clear(); pending = null;
    }

    /** The same reuse/create commit as end(), without touching the parent; attach() reconciles later. */
    void commit() {
        Map<String, Entry> old = current; current = next; next = old;
        next.clear(); pending = new ArrayList<>(wanted); wanted.clear();
    }

    boolean staged() { return pending != null; }

    /** Park the current sibling order for reattachment without running unchanged binders. */
    void detach() {
        pending = new ArrayList<>();
        for (int i = 0; i < parent.getChildCount(); i++) pending.add(parent.getChildAt(i));
        parent.removeAllViews();
    }

    /** The committed, detached views in sibling order; callers must not modify the list. */
    List<View> stagedViews() { return pending; }

    void attach() {
        if (pending == null) return;
        sync(parent, pending); pending = null;
    }

    static void sync(ViewGroup parent, List<? extends View> wanted) {
        Set<View> retained = Collections.newSetFromMap(new IdentityHashMap<>());
        retained.addAll(wanted);
        for (int i = parent.getChildCount() - 1; i >= 0; i--)
            if (!retained.contains(parent.getChildAt(i))) parent.removeViewAt(i);
        for (int i = 0; i < wanted.size(); i++) {
            View view = wanted.get(i);
            if (parent.getChildAt(i) == view) continue;
            if (view.getParent() == parent) parent.removeView(view);
            parent.addView(view, i);
        }
    }

}
