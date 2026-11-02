/* Android/native adapter of the TM4E Oniguruma boundary; EPL-2.0, see android/licenses/TM4E.txt. */
package org.eclipse.tm4e.core.internal.oniguruma;

public final class OnigResult {
    private final int[] captures;
    private int index;
    OnigResult(int[] captures, int index) { this.captures = captures; this.index = index; }
    public int getIndex() { return index; }
    void setIndex(int index) { this.index = index; }
    public int locationAt(int capture) { return Math.max(0, captures[capture * 2]); }
    public int count() { return captures.length / 2; }
    public int lengthAt(int capture) { return Math.max(0, captures[capture * 2 + 1] - captures[capture * 2]); }
    @Override public String toString() { return "OnigResult[" + index + "]"; }
}
