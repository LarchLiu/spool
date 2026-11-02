package com.cloudgeek.spool.textmate;

import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;

/** Small JNI boundary with explicit registry-scoped ownership, not finalizer-based native handles. */
public final class NativeOniguruma {
    private static final ThreadLocal<Pool> CURRENT = new ThreadLocal<>();
    static {
        String host = System.getProperty("spool.syntax.library");
        if (host == null) System.loadLibrary("spool-textmate"); else System.load(host);
    }
    private NativeOniguruma() {}
    public static final class Pool implements AutoCloseable {
        private final List<Regex> expressions = new ArrayList<>();
        private boolean closed;
        public synchronized Scope activate() {
            if (closed) throw new IllegalStateException("Disposed regex pool");
            Pool previous = CURRENT.get(); CURRENT.set(this); return new Scope(previous);
        }
        @Override public synchronized void close() { if (!closed) { closed = true; for (Regex expression : expressions) expression.close(); expressions.clear(); } }
        public synchronized int size() { return expressions.size(); }
    }
    public static final class Scope implements AutoCloseable {
        private final Pool previous;
        private Scope(Pool previous) { this.previous = previous; }
        @Override public void close() { if (previous == null) CURRENT.remove(); else CURRENT.set(previous); }
    }
    public static final class Regex implements AutoCloseable {
        private long pointer;
        private Regex(long pointer) { this.pointer = pointer; }
        public synchronized int[] search(byte[] text, int start) {
            if (pointer == 0) throw new IllegalStateException("Disposed regular expression");
            if (start < 0 || start > text.length) throw new IndexOutOfBoundsException();
            return nSearch(pointer, text, start);
        }
        @Override public synchronized void close() { if (pointer != 0) { nClose(pointer); pointer = 0; } }
    }
    public static Regex compile(String pattern, boolean ignoreCase) {
        Pool pool = CURRENT.get();
        if (pool == null) throw new IllegalStateException("Regex compilation requires an active owner");
        synchronized (pool) {
            if (pool.closed) throw new IllegalStateException("Disposed regex pool");
            Regex result = new Regex(nCompile(pattern.getBytes(StandardCharsets.UTF_8), ignoreCase));
            pool.expressions.add(result); return result;
        }
    }
    private static native long nCompile(byte[] pattern, boolean ignoreCase);
    private static native int[] nSearch(long pointer, byte[] text, int start);
    private static native void nClose(long pointer);
}
