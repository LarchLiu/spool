package com.cloudgeek.spool.android;

/** Web slash-commands.ts semantics, including ECMAScript whitespace and code-point limits. */
final class NativeGoalCommand {
    final String action, objective;
    private NativeGoalCommand(String action, String objective) { this.action = action; this.objective = objective; }

    static boolean suggestion(String text) {
        return text.equals("/") || text.equals("/g") || text.equals("/go") || text.equals("/goa") || text.equals("/goal");
    }

    static NativeGoalCommand parse(String text) {
        String value = trim(text);
        if (!value.startsWith("/goal") || value.length() > 5 && !space(value.charAt(5))) return null;
        String argument = trim(value.substring(5));
        if (argument.isEmpty()) return new NativeGoalCommand("get", "");
        switch (argument) {
            case "clear": case "stop": case "off": case "reset": case "none": case "cancel":
                return new NativeGoalCommand("clear", "");
            default: return new NativeGoalCommand("set", argument);
        }
    }

    String validationError(boolean attachments) {
        if (attachments) return "Goal commands do not accept attachments";
        if (objective.codePointCount(0, objective.length()) > 4000) return "Goal conditions must be at most 4000 characters";
        return "";
    }

    static String trim(String value) {
        int start = 0, end = value.length();
        while (start < end && space(value.charAt(start))) start++;
        while (end > start && space(value.charAt(end - 1))) end--;
        return value.substring(start, end);
    }

    private static boolean space(char value) {
        return value >= '\t' && value <= '\r' || value == ' ' || value == '\u00a0' || value == '\u1680'
                || value >= '\u2000' && value <= '\u200a' || value == '\u2028' || value == '\u2029'
                || value == '\u202f' || value == '\u205f' || value == '\u3000' || value == '\ufeff';
    }
}
