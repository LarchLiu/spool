package com.cloudgeek.spool.android;

import android.text.style.URLSpan;
import android.view.View;
import io.noties.markwon.AbstractMarkwonPlugin;
import io.noties.markwon.MarkwonVisitor;
import java.util.function.Consumer;
import org.commonmark.internal.util.Escaping;
import org.commonmark.node.*;
import org.commonmark.parser.IncludeSourceSpans;

/** Native file/image actions and the same source-aware GFM literal links as NativeMarkdown. */
final class NativeMarkwonLinks extends AbstractMarkwonPlugin {
    private final Consumer<String> open;
    private String source = "";
    NativeMarkwonLinks(Consumer<String> open) { this.open = open; }
    @Override public String processMarkdown(String markdown) { source = markdown; return markdown; }
    @Override public void configureParser(org.commonmark.parser.Parser.Builder builder) {
        builder.includeSourceSpans(IncludeSourceSpans.BLOCKS_AND_INLINES);
        builder.customInlineContentParserFactory(new NativeCodexFileCitation.Factory());
    }
    private void link(MarkwonVisitor visitor, int start, String target) {
        if (visitor.length() <= start || !NativeExternalLink.inline(target)) return;
        visitor.setSpans(start, new URLSpan(NativeGfmLinks.normalize(target)) {
            @Override public void onClick(View view) { open.accept(getURL()); }
        });
    }
    @Override public void configureVisitor(MarkwonVisitor.Builder builder) {
        builder.on(Link.class, (visitor, node) -> {
            int start = visitor.length(); visitor.visitChildren(node); link(visitor, start, node.getDestination());
        });
        builder.on(Image.class, (visitor, node) -> {
            int start = visitor.length(); visitor.visitChildren(node);
            if (visitor.length() == start) visitor.builder().append(node.getDestination());
            link(visitor, start, node.getDestination());
        });
        builder.on(NativeCodexFileCitation.class, (visitor, node) -> {
            if (nestedLink(node)) { visitor.builder().append(node.source); return; }
            int start = visitor.length(); visitor.builder().append(node.name); link(visitor, start, node.path.replace('\\', '/'));
        });
        builder.on(Text.class, (visitor, node) -> {
            if (nestedLink(node) || node.getSourceSpans().isEmpty()) { visitor.builder().append(node.getLiteral()); return; }
            StringBuilder raw = new StringBuilder();
            for (SourceSpan span : node.getSourceSpans()) raw.append(source, span.getInputIndex(), span.getInputIndex() + span.getLength());
            String value = raw.toString();
            java.util.List<NativeGfmLinks.Link> links = NativeGfmLinks.find(value);
            if (links.isEmpty()) { visitor.builder().append(node.getLiteral()); return; }
            int cursor = 0;
            for (NativeGfmLinks.Link literal : links) {
                visitor.builder().append(Escaping.unescapeString(value.substring(cursor, literal.start)));
                int start = visitor.length(); visitor.builder().append(value.substring(literal.start, literal.end));
                link(visitor, start, literal.target); cursor = literal.end;
            }
            visitor.builder().append(Escaping.unescapeString(value.substring(cursor)));
        });
    }
    private static boolean nestedLink(Node node) {
        for (Node parent = node.getParent(); parent != null; parent = parent.getParent())
            if (parent instanceof Link || parent instanceof Image) return true;
        return false;
    }
}
