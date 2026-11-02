package com.cloudgeek.spool.textmate;

import java.util.function.Supplier;

/** Android 8-compatible equivalent of the System.Logger operations used by pinned TM4E. */
public final class TextMatePlatform {
    private TextMatePlatform() {}
    public enum Level { ALL, TRACE, DEBUG, INFO, WARNING, ERROR, OFF }
    public interface Logger {
        boolean isLoggable(Level level);
        void log(Level level, String message);
        void log(Level level, String message, Throwable error);
        void log(Level level, String format, Object... arguments);
        default void log(Level level, Supplier<String> message) { if (isLoggable(level)) log(level, message.get()); }
    }
    public static Logger getLogger(String name) {
        java.util.logging.Logger logger = java.util.logging.Logger.getLogger(name);
        return new Logger() {
            private java.util.logging.Level mapped(Level level) {
                switch (level) {
                    case ALL: return java.util.logging.Level.ALL;
                    case TRACE: return java.util.logging.Level.FINER;
                    case DEBUG: return java.util.logging.Level.FINE;
                    case WARNING: return java.util.logging.Level.WARNING;
                    case ERROR: return java.util.logging.Level.SEVERE;
                    case OFF: return java.util.logging.Level.OFF;
                    default: return java.util.logging.Level.INFO;
                }
            }
            public boolean isLoggable(Level level) { return logger.isLoggable(mapped(level)); }
            public void log(Level level, String message) { logger.log(mapped(level), message); }
            public void log(Level level, String message, Throwable error) { logger.log(mapped(level), message, error); }
            public void log(Level level, String format, Object... arguments) { logger.log(mapped(level), format, arguments); }
        };
    }
    public static long currentTimeMillis() { return System.currentTimeMillis(); }
    public static String lineSeparator() { return System.lineSeparator(); }
    public static boolean isEmpty(CharSequence text) { return text.length() == 0; }
    public static void arraycopy(Object from, int fromIndex, Object to, int toIndex, int count) { System.arraycopy(from, fromIndex, to, toIndex, count); }
}
