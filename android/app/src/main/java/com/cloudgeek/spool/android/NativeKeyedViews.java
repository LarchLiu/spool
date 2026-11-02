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

    NativeKeyedViews(ViewGroup parent) { this.parent = parent; }

    void begin() { next.clear(); wanted.clear(); }

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
        next.clear(); wanted.clear();
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
