package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.Typeface;
import android.text.SpannableStringBuilder;
import android.text.Spanned;
import android.text.method.LinkMovementMethod;
import android.text.style.ReplacementSpan;
import android.text.style.StrikethroughSpan;
import android.text.style.StyleSpan;
import android.view.Gravity;
import android.view.View;
import android.widget.HorizontalScrollView;
import android.widget.LinearLayout;
import android.widget.TextView;

import org.commonmark.Extension;
import org.commonmark.ext.gfm.strikethrough.Strikethrough;
import org.commonmark.ext.gfm.strikethrough.StrikethroughExtension;
import org.commonmark.ext.gfm.tables.TableBlock;
import org.commonmark.ext.gfm.tables.TableCell;
import org.commonmark.ext.gfm.tables.TablesExtension;
import org.commonmark.ext.task.list.items.TaskListItemMarker;
import org.commonmark.ext.task.list.items.TaskListItemsExtension;
import org.commonmark.node.*;
import org.commonmark.parser.Parser;
import org.commonmark.parser.IncludeSourceSpans;
import org.commonmark.internal.util.Escaping;

import java.util.Arrays;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.function.Consumer;

/** CommonMark/GFM AST to native views. HTML is skipped; images are explicit links, never requests. */
final class NativeMarkdown {
    private static final List<Extension> EXTENSIONS = Arrays.asList(TablesExtension.create(),
            StrikethroughExtension.create(), TaskListItemsExtension.create());
    private static final Parser PARSER = Parser.builder().extensions(EXTENSIONS)
            .customInlineContentParserFactory(new NativeCodexFileCitation.Factory())
            .includeSourceSpans(IncludeSourceSpans.BLOCKS_AND_INLINES).build();
    private final NativeUi ui;
    private final Consumer<String> open;
    private final float lineHeight;
    private final int fontSize;
    private final int codeContainerSize;
    private boolean preview;
    private boolean normalWhitespace;
    private String source;
    private TextView inlineView;
    private Flow document;

    private static final class Root extends NativeMarkdownFlow {
        final NativeMarkdown renderer;
        Root(NativeMarkdown renderer) { super(renderer.ui); this.renderer = renderer; }
    }
    private static final class Flow {
        final NativeMarkdownFlow view;
        Map<String, Bound> children = new HashMap<>();
        Map<String, Integer> positions = new HashMap<>();
        Flow(NativeMarkdownFlow view) { this.view = view; }
    }
    private static final class Bound {
        View view;
        String signature = "", marker = "";
        boolean checked;
        Flow flow;
        Map<String, Bound> children = new HashMap<>();
    }

    private Node parse(String source) { this.source = source; return PARSER.parse(source); }

    private NativeMarkdown(NativeUi ui, boolean mine, Consumer<String> open) {
        this(ui, mine, open, 14);
    }

    private NativeMarkdown(NativeUi ui, boolean mine, Consumer<String> open, int fontSize) {
        this(ui, open, fontSize, mine ? 1.75f : 1.85f);
    }

    private NativeMarkdown(NativeUi ui, Consumer<String> open, int fontSize, float lineHeight) {
        this(ui, open, fontSize, lineHeight, fontSize);
    }

    private NativeMarkdown(NativeUi ui, Consumer<String> open, int fontSize, float lineHeight, int codeContainerSize) {
        this.ui = ui;
        this.open = open;
        this.lineHeight = lineHeight;
        this.fontSize = fontSize;
        this.codeContainerSize = codeContainerSize;
    }

    static LinearLayout view(NativeUi ui, String source, boolean mine, Consumer<String> open) {
        return render(new NativeMarkdown(ui, mine, open), source);
    }

    static LinearLayout detail(NativeUi ui, String source, Consumer<String> open) {
        return render(new NativeMarkdown(ui, open, 13, 1.2f, 12), source);
    }

    static LinearLayout filePreview(NativeUi ui, String source, Consumer<String> open) {
        return render(new NativeMarkdown(ui, open, 13, 1.2f), source);
    }

    static LinearLayout progress(NativeUi ui, String source, Consumer<String> open) {
        return render(new NativeMarkdown(ui, open, 14, 1.2f), source);
    }

    private static LinearLayout render(NativeMarkdown renderer, String source) {
        Root root = new Root(renderer);
        renderer.document = new Flow(root);
        renderer.update(source);
        return root;
    }

    static void bind(View root, String source) {
        if (!(root instanceof Root)) throw new IllegalArgumentException("Not a native Markdown root");
        ((Root) root).renderer.update(source);
    }

    private void update(String value) {
        if (value.equals(source)) return;
        blocks(parse(value), ui.text, false, document);
    }

    static CharSequence preview(NativeUi ui, String source) {
        NativeMarkdown renderer = new NativeMarkdown(ui, false, ignored -> {});
        renderer.preview = true;
        SpannableStringBuilder text = new SpannableStringBuilder();
        renderer.appendChildren(renderer.parse(source), text, false);
        return text;
    }

    private float px(float value) { return value * ui.context.getResources().getDisplayMetrics().density; }

    private Flow blocks(Node parent, int color, boolean tight, Flow state) {
        if (state == null) state = new Flow(new NativeMarkdownFlow(ui));
        NativeMarkdownFlow column = state.view;
        column.zeroFirst = parent instanceof Document;
        column.zeroLast = parent instanceof Document || parent instanceof BlockQuote;
        Map<String, Integer> counts = new HashMap<>();
        Map<String, Bound> next = new HashMap<>();
        List<NativeMarkdownFlow.Item> items = new ArrayList<>();
        List<String> keys = new ArrayList<>();
        for (Node node = parent.getFirstChild(); node != null; node = node.getNext()) {
            String kind = kind(node, tight);
            if (kind.isEmpty()) continue;
            int index = counts.getOrDefault(kind, 0); counts.put(kind, index + 1);
            String key = kind + ":" + index;
            Bound bound = state.children.getOrDefault(key, new Bound());
            String signature = sourceKey(node, color, tight);
            if (!signature.equals(bound.signature)) {
                bound.view = block(node, color, bound);
                bound.signature = signature;
            }
            View block = bound.view;
            if (node instanceof Paragraph && !tight) element(block, "p");
            if (node instanceof Heading) element(block, "h" + ((Heading) node).getLevel());
            float before = before(node), after = after(node);
            if (node instanceof Paragraph && parent instanceof ListItem) after = tight ? 0 : fontSize * .35f;
            items.add(new NativeMarkdownFlow.Item(block, px(before), px(after)));
            next.put(key, bound);
            keys.add(key);
        }
        reconcileSelection(state, next, keys, parent instanceof BlockQuote || parent instanceof ListItem && !tight);
        column.replace(items); state.children = next;
        return state;
    }

    private static void reconcileSelection(Flow state, Map<String, Bound> next, List<String> keys, boolean edges) {
        List<String> order = new ArrayList<>();
        if (edges && !keys.isEmpty()) order.add("@text:0");
        for (int i = 0; i < keys.size(); i++) {
            order.add(keys.get(i));
            if (edges || i + 1 < keys.size()) order.add("@text:" + order.size());
        }
        Map<String, Integer> positions = new HashMap<>();
        int placed = 0;
        for (int i = 0; i < order.size(); i++) {
            String key = order.get(i);Integer previous = state.positions.get(key);
            // React can move a keyed element past a reused, unkeyed newline text node.
            // Chrome collapses selections in moved DOM subtrees, even if the element survives.
            if (previous != null) {
                if (previous < placed && next.containsKey(key)) clearSelection(next.get(key).view);
                else placed = Math.max(placed, previous);
            }
            positions.put(key, i);
        }
        state.positions = positions;
    }

    private static void clearSelection(View view) {
        if (view instanceof TextView && ((TextView) view).getText() instanceof android.text.Spannable)
            android.text.Selection.removeSelection((android.text.Spannable) ((TextView) view).getText());
        if (view instanceof android.view.ViewGroup) {
            android.view.ViewGroup group = (android.view.ViewGroup) view;
            for (int i = 0; i < group.getChildCount(); i++) clearSelection(group.getChildAt(i));
        }
    }

    private String sourceKey(Node node, int color, boolean tight) {
        StringBuilder key = new StringBuilder().append(color).append('/').append(tight).append('/').append(node.getClass().getName());
        for (SourceSpan span : node.getSourceSpans()) key.append('\n').append(source, span.getInputIndex(), span.getInputIndex() + span.getLength());
        resolvedKey(node, key);
        return key.toString();
    }

    private void resolvedKey(Node node, StringBuilder key) {
        key.append('/').append(node.getClass().getName());
        String value = node instanceof Link ? ((Link) node).getDestination() : node instanceof Image ? ((Image) node).getDestination()
                : node instanceof Text ? ((Text) node).getLiteral() : node instanceof Code ? ((Code) node).getLiteral()
                : node instanceof FencedCodeBlock ? ((FencedCodeBlock) node).getLiteral()
                : node instanceof IndentedCodeBlock ? ((IndentedCodeBlock) node).getLiteral() : "";
        // Reference definitions can change a destination outside the block's own source spans.
        key.append(':').append(value.length()).append(':').append(value);
        if (node instanceof ListBlock) key.append(':').append(((ListBlock) node).isTight());
        if (node instanceof TaskListItemMarker) key.append(':').append(((TaskListItemMarker) node).isChecked());
        if (node instanceof TableCell) key.append(':').append(((TableCell) node).getAlignment());
        for (Node child = node.getFirstChild(); child != null; child = child.getNext()) resolvedKey(child, key);
    }

    // ReactMarkdown's generated sibling keys count occurrences of each HTML tag.
    private static String kind(Node node, boolean tight) {
        if (node instanceof Paragraph) return tight ? "inline" : "p";
        if (node instanceof Heading) return "h" + ((Heading) node).getLevel();
        if (node instanceof FencedCodeBlock || node instanceof IndentedCodeBlock) return "pre";
        if (node instanceof BlockQuote) return "blockquote";
        if (node instanceof OrderedList) return "ol";
        if (node instanceof BulletList) return "ul";
        if (node instanceof TableBlock) return "table";
        return node instanceof ThematicBreak ? "hr" : "";
    }

    private static <T extends View> T element(T view, String name) {
        view.setTag("markdown:" + name);
        return view;
    }

    private float before(Node node) {
        if (node instanceof Heading) return fontSize * headingScale((Heading) node) * 1.2f;
        if (node instanceof ListBlock) return fontSize * (node.getParent() instanceof ListItem ? .3f : .6f);
        if (node instanceof BlockQuote || node instanceof FencedCodeBlock || node instanceof IndentedCodeBlock) return fontSize * .9f;
        if (node instanceof TableBlock) return fontSize;
        if (node instanceof ThematicBreak) return fontSize * 1.4f;
        return 0;
    }

    private float after(Node node) {
        if (node instanceof Heading) return fontSize * headingScale((Heading) node) * .55f;
        if (node instanceof ListBlock) return fontSize * (node.getParent() instanceof ListItem ? .3f : 1);
        if (node instanceof TableBlock) return fontSize;
        if (node instanceof FencedCodeBlock || node instanceof IndentedCodeBlock) return fontSize * 1.1f;
        if (node instanceof ThematicBreak) return fontSize * 1.4f;
        return fontSize * .9f;
    }

    private float headingScale(Heading node) {
        return node.getLevel() == 1 ? 1.65f : node.getLevel() == 2 ? 1.4f : node.getLevel() == 3 ? 1.18f : 1;
    }

    private View block(Node node, int color, Bound bound) {
        if (node instanceof Paragraph || node instanceof Heading) {
            boolean fresh = bound.view == null;
            TextView view = inline(node, color, (TextView) bound.view);
            if (fresh && node instanceof Heading) {
                float size = fontSize * headingScale((Heading) node);
                ui.typography(view, Math.round(size), color, 650);
                view.setTextSize(size);
                ui.cssLineHeight(view, 1.4f);
                view.setLetterSpacing(-.02f);
            }
            return view;
        }
        if (node instanceof FencedCodeBlock || node instanceof IndentedCodeBlock) {
            String literal = node instanceof FencedCodeBlock ? ((FencedCodeBlock) node).getLiteral()
                    : ((IndentedCodeBlock) node).getLiteral();
            String info = node instanceof FencedCodeBlock ? ((FencedCodeBlock) node).getInfo() : "";
            NativeCodeBlock code = bound.view == null ? new NativeCodeBlock(ui, color, codeContainerSize) : (NativeCodeBlock) bound.view;
            code.bind(info, literal);
            return code;
        }
        if (node instanceof BlockQuote) {
            bound.flow = blocks(node, ui.muted, false, bound.flow);
            NativeMarkdownFlow contents = bound.flow.view;
            contents.collapseTop = contents.collapseBottom = false;
            contents.inset(px(3 + fontSize), px(fontSize * .25f), 0, px(fontSize * .25f));
            contents.leftBorder(px(3), ui.borderStrong);
            return element(contents, "blockquote");
        }
        if (node instanceof ListBlock) return list((ListBlock) node, color, bound);
        if (node instanceof TableBlock) return table(node, color, bound);
        if (node instanceof ThematicBreak) {
            if (bound.view != null) return bound.view;
            View rule = new View(ui.context) {
                @Override protected void onMeasure(int width, int height) { setMeasuredDimension(MeasureSpec.getSize(width), ui.dp(1)); }
            };
            rule.setBackgroundColor(ui.line);
            return element(rule, "hr");
        }
        return null;
    }

    private View list(ListBlock list, int color, Bound bound) {
        NativeMarkdownFlow column = bound.view == null ? new NativeMarkdownFlow(ui) : (NativeMarkdownFlow) bound.view;
        Map<String, Bound> next = new HashMap<>();
        List<NativeMarkdownFlow.Item> rows = new ArrayList<>();
        boolean ordered = list instanceof OrderedList;
        int depth = 1;
        for (Node parent = list.getParent(); parent != null; parent = parent.getParent()) if (parent instanceof ListBlock) depth++;
        String style = ordered ? "decimal" : depth == 1 ? "disc" : depth == 2 ? "circle" : "square";
        boolean tasks = false;
        for (Node item = list.getFirstChild(); item != null; item = item.getNext())
            for (Node child = item.getFirstChild(); child != null; child = child.getNext()) if (child instanceof TaskListItemMarker) tasks = true;
        column.inset(tasks ? 0 : px(fontSize * 1.7f), 0, 0, 0);
        int number = list instanceof OrderedList ? ((OrderedList) list).getMarkerStartNumber() : 0;
        int index = 0;
        for (Node item = list.getFirstChild(); item != null; item = item.getNext()) {
            String key = "li:" + index++;
            Bound rowState = bound.children.getOrDefault(key, new Bound());
            TaskListItemMarker task = null;
            for (Node child = item.getFirstChild(); child != null; child = child.getNext())
                if (child instanceof TaskListItemMarker) task = (TaskListItemMarker) child;
            rowState.flow = blocks(item, color, list.isTight(), rowState.flow);
            NativeMarkdownListItem row = rowState.view == null ? new NativeMarkdownListItem(ui, rowState.flow.view, px(fontSize * 1.7f)) : (NativeMarkdownListItem) rowState.view;
            row.markerType = tasks ? "none" : style;
            rowState.checked = task != null && task.isChecked();
            String marker = task != null ? "task" : tasks ? "none" : ordered ? "number:" + number : style;
            if (!marker.equals(rowState.marker) && task != null) {
                final Bound checkedState = rowState;
                android.widget.FrameLayout mark = new android.widget.FrameLayout(ui.context) {
                    @Override public void onInitializeAccessibilityNodeInfo(android.view.accessibility.AccessibilityNodeInfo info) {
                        super.onInitializeAccessibilityNodeInfo(info);
                        info.setClassName(android.widget.CheckBox.class.getName());
                        info.setCheckable(true); info.setChecked(checkedState.checked); info.setEnabled(false);
                    }
                };
                mark.setBackground(NativeCheckbox.glyph(ui, () -> checkedState.checked, () -> false));
                mark.setEnabled(false);
                mark.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_YES);
                row.task(element(mark, "input"), ui.dp(13), ui.dp(20));
            } else if (!marker.equals(rowState.marker) && !tasks) {
                if (ordered) {
                    TextView bullet = ui.text(number + ".", fontSize, color);
                    ui.cssLineHeight(bullet, lineHeight); ui.preciseText(bullet, true);
                    bullet.setSingleLine(true); bullet.setHorizontallyScrolling(true);
                    bullet.setGravity(Gravity.END); bullet.setPadding(0, 0, ui.dp(4), 0);
                    row.marker(bullet);
                } else row.marker(new NativeMarkdownBullet(ui, px(fontSize), px(fontSize * lineHeight), style, color));
            } else if (!marker.equals(rowState.marker)) row.marker(null);
            if (task != null) row.refreshMarker();
            rowState.marker = marker; rowState.view = row; next.put(key, rowState);
            rows.add(new NativeMarkdownFlow.Item(element(row, "li"), px(fontSize * .25f), px(fontSize * .25f)));
            if (ordered) number++;
        }
        column.replace(rows); bound.children = next;
        return element(column, list instanceof OrderedList ? "ol" : "ul");
    }

    private View table(Node node, int color, Bound bound) {
        NativeMarkdownTable table = bound.view == null ? new NativeMarkdownTable(ui) : (NativeMarkdownTable) ((HorizontalScrollView) bound.view).getChildAt(0);
        Map<String, Bound> next = new HashMap<>();
        List<NativeMarkdownTable.Row> rows = new ArrayList<>();
        for (Node section = node.getFirstChild(); section != null; section = section.getNext()) {
            int rowIndex = 0;
            for (Node row = section.getFirstChild(); row != null; row = row.getNext()) {
                String rowKey = section.getClass().getName() + ":" + rowIndex++;
                Bound rowState = bound.children.getOrDefault(rowKey, new Bound());
                NativeMarkdownTable.Row cells = rowState.view == null ? new NativeMarkdownTable.Row(ui) : (NativeMarkdownTable.Row) rowState.view;
                Map<String, Bound> nextCells = new HashMap<>();
                List<TextView> texts = new ArrayList<>();
                int cellIndex = 0;
                for (Node cell = row.getFirstChild(); cell != null; cell = cell.getNext()) {
                    if (!(cell instanceof TableCell)) continue;
                    TableCell value = (TableCell) cell;
                    String cellKey = (value.isHeader() ? "th:" : "td:") + cellIndex++;
                    Bound cellState = rowState.children.getOrDefault(cellKey, new Bound());
                    String signature = sourceKey(cell, color, false) + "/" + value.getAlignment();
                    TextView text = (TextView) cellState.view;
                    if (!signature.equals(cellState.signature)) {
                        boolean fresh = text == null;
                        text = inline(cell, color, text);
                        if (fresh) {
                            if (value.isHeader()) ui.typography(text, 13, color, 650);
                            text.setTextSize(fontSize * .92f);
                            ui.cssLineHeight(text, lineHeight);
                            text.setPadding(ui.dp(12), ui.dp(7), ui.dp(12), ui.dp(8));
                            text.setBackgroundColor(value.isHeader() ? ui.soft : 0x00000000);
                            NativeCssWrap.install(text);
                        }
                        text.setGravity(Gravity.CENTER_VERTICAL | (value.getAlignment() == TableCell.Alignment.RIGHT ? Gravity.END
                                : value.getAlignment() == TableCell.Alignment.CENTER || value.isHeader() && value.getAlignment() == null
                                ? Gravity.CENTER_HORIZONTAL : Gravity.START));
                        cellState.signature = signature; cellState.view = text;
                    }
                    texts.add(element(text, value.isHeader() ? "th" : "td")); nextCells.put(cellKey, cellState);
                }
                cells.cells(texts); rowState.view = cells; rowState.children = nextCells; next.put(rowKey, rowState);
                rows.add(element(cells, "tr"));
            }
        }
        table.rows(rows); bound.children = next;
        return bound.view == null ? element(horizontal(element(table, "table")), "table-scroll") : bound.view;
    }

    private HorizontalScrollView horizontal(View child) {
        HorizontalScrollView scroll = new MarkdownScroll(ui, child);
        scroll.setHorizontalScrollBarEnabled(false);
        scroll.addView(child, new android.widget.FrameLayout.LayoutParams(-2, -2));
        return scroll;
    }

    private static final class MarkdownScroll extends HorizontalScrollView implements NativeMarkdownFlowBox {
        private final View content;
        MarkdownScroll(NativeUi ui, View content) { super(ui.context); this.content = content; }
        @Override protected void onMeasure(int width, int height) {
            if (content instanceof NativeMarkdownTable) ((NativeMarkdownTable) content).availableWidth(MeasureSpec.getSize(width));
            super.onMeasure(width, height);
        }
        @Override public float cssHeight() { return NativeMarkdownFlow.contentHeight(content); }
        @Override public void roundingOffset(float x, float y) {
            if (content instanceof NativeMarkdownFlowBox) ((NativeMarkdownFlowBox) content).roundingOffset(x, y);
        }
        @Override public void layoutRounding() {
            if (content instanceof NativeMarkdownFlowBox) ((NativeMarkdownFlowBox) content).layoutRounding();
        }
    }

    private TextView inline(Node parent, int color, TextView text) {
        boolean fresh = text == null;
        if (fresh) text = ui.text("", fontSize, color);
        inlineView = text;
        if (fresh) {
            ui.preciseText(text, true);
            ui.cssLineHeight(text, lineHeight);
            text.setBreakStrategy(android.graphics.text.LineBreaker.BREAK_STRATEGY_SIMPLE);
            text.setHyphenationFrequency(android.text.Layout.HYPHENATION_FREQUENCY_NONE);
        }
        SpannableStringBuilder out = new SpannableStringBuilder();
        boolean oldWhitespace = normalWhitespace;
        normalWhitespace = !(parent instanceof Paragraph) || parent.getParent() instanceof ListItem
                && parent.getParent().getParent() instanceof ListBlock && ((ListBlock) parent.getParent().getParent()).isTight();
        appendChildren(parent, out, true);
        normalWhitespace = oldWhitespace;
        if (fresh) text.setText(out); else ((NativeTextView) text).bindMarkdownText(out);
        if (fresh) {
            text.setTextIsSelectable(true);
            text.setMovementMethod(LinkMovementMethod.getInstance());
        }
        return text;
    }

    private void appendChildren(Node parent, SpannableStringBuilder out, boolean autolinks) {
        for (Node node = parent.getFirstChild(); node != null; node = node.getNext()) append(node, out, autolinks);
    }

    private void append(Node node, SpannableStringBuilder out, boolean autolinks) {
        int start = out.length();
        if (node instanceof NativeCodexFileCitation) {
            NativeCodexFileCitation file = (NativeCodexFileCitation) node;
            boolean nested = false;
            for (Node parent = node.getParent(); parent != null; parent = parent.getParent())
                if (parent instanceof Link || parent instanceof Image) { nested = true; break; }
            out.append(nested ? file.source : file.name);
            if (!preview && !nested) link(out, file.path.replace('\\', '/'), start, out.length());
        } else if (node instanceof Text) {
            String literal = ((Text) node).getLiteral();
            if (autolinks && !preview && appendLiteralLinks(node, out)) return;
            if (normalWhitespace) literal = literal.replaceAll("[ \\t\\r\\n\\f]+", " ");
            out.append(preview ? literal.replaceAll("\\s+", " ") : literal);
        } else if (node instanceof Code) {
            out.append(((Code) node).getLiteral());
            // A ReplacementSpan is atomic; long code must remain line-breakable like CSS break-spaces.
            if (out.length() - start <= 18) span(out, new InlineCode(ui), start, out.length());
            else {
                span(out, new android.text.style.TypefaceSpan("monospace"), start, out.length());
                span(out, new android.text.style.RelativeSizeSpan(.88f), start, out.length());
                span(out, new android.text.style.BackgroundColorSpan(ui.soft), start, out.length());
            }
        } else if (preview && (node instanceof FencedCodeBlock || node instanceof IndentedCodeBlock)) {
            out.append(node instanceof FencedCodeBlock ? ((FencedCodeBlock) node).getLiteral() : ((IndentedCodeBlock) node).getLiteral());
            span(out, new android.text.style.TypefaceSpan("monospace"), start, out.length());
            out.append(' ');
        } else if (node instanceof SoftLineBreak) out.append(preview || normalWhitespace ? ' ' : '\n');
        else if (node instanceof HardLineBreak) {
            // React emits <br> plus a newline, which pre-wrap paragraphs preserve as a second break.
            out.append(preview ? " " : normalWhitespace ? "\n" : "\n\n");
        } else if (preview && node instanceof ThematicBreak) out.append(' ');
        else if (node instanceof HtmlInline || node instanceof HtmlBlock || node instanceof TaskListItemMarker) return;
        else {
            appendChildren(node, out, autolinks && !(node instanceof Link || node instanceof Image));
            if (node instanceof StrongEmphasis || preview && node instanceof Heading) span(out, new StyleSpan(Typeface.BOLD), start, out.length());
            if (node instanceof Emphasis) span(out, new StyleSpan(Typeface.ITALIC), start, out.length());
            if (node instanceof Strikethrough) span(out, new StrikethroughSpan(), start, out.length());
            if (node instanceof Link && !preview) link(out, ((Link) node).getDestination(), start, out.length());
            if (node instanceof Image) {
                String url = ((Image) node).getDestination();
                if (out.length() == start && !preview) out.append(url);
                if (!preview) link(out, url, start, out.length());
            }
            if (preview && (node instanceof Paragraph || node instanceof Heading || node instanceof ListItem
                    || node instanceof BlockQuote || node instanceof TableCell)) out.append(' ');
        }
    }

    private boolean appendLiteralLinks(Node node, SpannableStringBuilder out) {
        StringBuilder raw = new StringBuilder();
        for (SourceSpan span : node.getSourceSpans()) raw.append(source, span.getInputIndex(), span.getInputIndex() + span.getLength());
        String value = raw.toString();
        List<NativeGfmLinks.Link> links = NativeGfmLinks.find(value);
        if (links.isEmpty()) return false;
        int cursor = 0;
        for (NativeGfmLinks.Link literal : links) {
            String before = Escaping.unescapeString(value.substring(cursor, literal.start));
            out.append(normalWhitespace ? before.replaceAll("[ \\t\\r\\n\\f]+", " ") : before);
            int start = out.length();
            out.append(value, literal.start, literal.end);
            link(out, literal.target, start, out.length());
            cursor = literal.end;
        }
        String after = Escaping.unescapeString(value.substring(cursor));
        out.append(normalWhitespace ? after.replaceAll("[ \\t\\r\\n\\f]+", " ") : after);
        return true;
    }

    private void link(SpannableStringBuilder out, String target, int start, int end) {
        if (!NativeExternalLink.inline(target)) return;
        String address = NativeGfmLinks.normalize(target);
        span(out, new NativeMarkdownLink(inlineView, ui.primary, address, open), start, end);
    }

    private static void span(SpannableStringBuilder text, Object span, int start, int end) {
        if (end > start) text.setSpan(span, start, end, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
    }

    private static final class InlineCode extends ReplacementSpan {
        private final NativeUi ui;
        InlineCode(NativeUi ui) { this.ui = ui; }
        private Paint paint(Paint original) {
            Paint paint = new Paint(original);
            paint.setTypeface(Typeface.MONOSPACE);
            paint.setTextSize(original.getTextSize() * .88f);
            return paint;
        }
        @Override public int getSize(Paint original, CharSequence text, int start, int end, Paint.FontMetricsInt metrics) {
            Paint code = paint(original);
            return Math.round(code.measureText(text, start, end) + original.getTextSize() * .7f);
        }
        @Override public void draw(Canvas canvas, CharSequence text, int start, int end, float x, int top, int y, int bottom, Paint original) {
            Paint code = paint(original);
            float width = getSize(original, text, start, end, null);
            float padding = original.getTextSize() * .15f;
            float above = y + code.ascent() - padding, below = y + code.descent() + padding;
            code.setColor(ui.soft);
            canvas.drawRoundRect(x, above, x + width, below, ui.dp(5), ui.dp(5), code);
            code.setStyle(Paint.Style.STROKE);
            code.setStrokeWidth(ui.dp(1));
            code.setColor(ui.line);
            canvas.drawRoundRect(x, above, x + width, below, ui.dp(5), ui.dp(5), code);
            code.setStyle(Paint.Style.FILL);
            code.setColor(original.getColor());
            if (text instanceof Spanned) for (NativeMarkdownLink link : ((Spanned) text).getSpans(start, end, NativeMarkdownLink.class)) {
                link.code(canvas, x + original.getTextSize() * .35f, x + width - original.getTextSize() * .35f, y);
                code.setColor(link.color());
            }
            canvas.drawText(text, start, end, x + original.getTextSize() * .35f, y, code);
        }
    }
}
