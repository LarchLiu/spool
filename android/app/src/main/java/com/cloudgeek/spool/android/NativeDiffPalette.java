package com.cloudgeek.spool.android;

/** Generated from Pierre's pinned Web themes and CSS Lab color mixes. */
final class NativeDiffPalette {
    final int bg;
    final int fg;
    final int added;
    final int deleted;
    final int modified;
    final int number;
    final int additionBg;
    final int deletionBg;
    final int additionEmphasis;
    final int deletionEmphasis;
    final int additionGutter;
    final int deletionGutter;
    final int buffer;
    final int separator;
    NativeDiffPalette(boolean dark) {
        bg = dark ? 0xff0a0a0a : 0xffffffff;
        fg = dark ? 0xfffafafa : 0xff0a0a0a;
        added = dark ? 0xff07c480 : 0xff18a46c;
        deleted = dark ? 0xffff2e3f : 0xffd52c36;
        modified = dark ? 0xff009fff : 0xff009fff;
        number = dark ? 0xff9d9d9d : 0xff575757;
        additionBg = dark ? 0xff182d22 : 0xffe7f4ec;
        deletionBg = dark ? 0xff381917 : 0xffffe8e4;
        additionEmphasis = dark ? 0xff154b35 : 0xffc8e8d9;
        deletionEmphasis = dark ? 0xff601d1f : 0xfff9ccca;
        additionGutter = dark ? 0xff16241c : 0xffedf7f1;
        deletionGutter = dark ? 0xff2d1714 : 0xffffeeeb;
        buffer = dark ? 0xff1d1d1d : 0xffe8e8e8;
        separator = dark ? 0xff2b2b2b : 0xfff3f3f3;
    }
}
