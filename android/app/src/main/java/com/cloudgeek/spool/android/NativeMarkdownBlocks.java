package com.cloudgeek.spool.android;

import android.text.Spanned;
import org.commonmark.node.Node;
import org.commonmark.node.SourceSpan;
import org.commonmark.node.Link;
import org.commonmark.node.Image;
import org.commonmark.ext.gfm.tables.TableCell;
import org.commonmark.ext.task.list.items.TaskListItemMarker;

/** Immutable block results; retaining a block also retains its selectable document and identity. */
final class NativeMarkdownBlocks {
    static final class Block {
        final long id;
        final String key;
        final Node node;
        final Spanned text;
        Block(long id, String key, Node node, Spanned text) { this.id = id; this.key = key; this.node = node; this.text = text; }
    }
    static String key(Node node, String source) {
        StringBuilder result = new StringBuilder();
        for (SourceSpan span : node.getSourceSpans()) {
            result.append(span.getLength()).append(':').append(source, span.getInputIndex(), span.getInputIndex() + span.getLength());
        }
        // Sibling boundaries affect Markwon's leading/trailing newlines.
        result.append('/').append(node.getPrevious() != null).append('/').append(node.getNext() != null);
        resolved(node, result);
        return result.toString();
    }
    private static void resolved(Node node, StringBuilder result) {
        String value = node.toString();
        result.append('/').append(value.length()).append(':').append(value);
        // Include resolved references even when their definitions lie outside this block.
        if (node instanceof Link) append(result, ((Link) node).getDestination());
        if (node instanceof Image) append(result, ((Image) node).getDestination());
        if (node instanceof TableCell) result.append(':').append(((TableCell) node).getAlignment());
        if (node instanceof TaskListItemMarker) result.append(':').append(((TaskListItemMarker) node).isChecked());
        if (node instanceof NativeCodexFileCitation) append(result, ((NativeCodexFileCitation) node).path);
        for (Node child = node.getFirstChild(); child != null; child = child.getNext()) resolved(child, result);
    }
    private static void append(StringBuilder result, String value) { result.append(':').append(value.length()).append(':').append(value); }
}
