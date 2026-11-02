/* Android/native adapter of the TM4E Oniguruma boundary; EPL-2.0, see android/licenses/TM4E.txt. */
package org.eclipse.tm4e.core.internal.oniguruma;

import com.cloudgeek.spool.textmate.NativeOniguruma;

public final class OnigRegExp {
    private final String pattern;
    private final NativeOniguruma.Regex regex;
    private final boolean hasGAnchor;
    private OnigString previous;
    private int previousPosition;
    private OnigResult previousResult;
    public OnigRegExp(String pattern) { this(pattern, false); }
    public OnigRegExp(String pattern, boolean ignoreCase) {
        this.pattern = pattern; hasGAnchor = pattern.contains("\\G");
        regex = NativeOniguruma.compile(pattern, ignoreCase);
    }
    public OnigResult search(OnigString text, int position) {
        if (!hasGAnchor && previous == text && previousPosition <= position && (previousResult == null || previousResult.locationAt(0) >= position)) return previousResult;
        int[] captures = regex.search(text.bytesUTF8, position);
        previous = text; previousPosition = position;
        previousResult = captures == null ? null : new OnigResult(captures, -1);
        return previousResult;
    }
    public String pattern() { return pattern; }
    @Override public String toString() { return "OnigRegExp[" + pattern + "]"; }
}
