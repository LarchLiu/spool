package com.cloudgeek.spool.android;

/** Generated from the same pinned art definitions as Web avatar metadata. */
final class NativeAvatarCatalog {
    static final String[] STYLES = {"clay", "pixel-art", "croodles", "lorelei", "notionists", "toon-head", "voxel-art", "thumbs"};
    static final String[] LABELS = {"Clay", "Pixel Art", "Croodles", "Lorelei", "Notionists", "Toon Head", "Voxel Art", "Thumbs"};
    static final String[][] CREDITS = {
        {"Clay", "https://www.dicebear.com", "DiceBear", "https://www.dicebear.com", "CC0 1.0", "https://creativecommons.org/publicdomain/zero/1.0/"},
        {"Pixel Art", "https://www.figma.com/community/file/1198754108850888330", "DiceBear", "https://www.dicebear.com", "CC0 1.0", "https://creativecommons.org/publicdomain/zero/1.0/"},
        {"Croodles - Doodle your face", "https://www.figma.com/community/file/966199982810283152", "vijay verma", "https://vjy.me/", "CC BY 4.0", "https://creativecommons.org/licenses/by/4.0/"},
        {"Lorelei", "https://www.figma.com/community/file/1198749693280469639", "Lisa Wischofsky", "https://www.instagram.com/lischi_art/", "CC0 1.0", "https://creativecommons.org/publicdomain/zero/1.0/"},
        {"Notionists", "https://heyzoish.gumroad.com/l/notionists", "Zoish", "https://bio.link/heyzoish", "CC0 1.0", "https://creativecommons.org/publicdomain/zero/1.0/"},
        {"ToonHead", "https://www.figma.com/community/file/1589627891082866389", "Johan Melin", "https://www.johanmelin.com", "CC BY 4.0", "https://creativecommons.org/licenses/by/4.0/"},
        {"Voxel Art", "https://www.dicebear.com", "DiceBear", "https://www.dicebear.com", "CC0 1.0", "https://creativecommons.org/publicdomain/zero/1.0/"},
        {"Thumbs", "https://www.dicebear.com", "DiceBear", "https://www.dicebear.com", "CC0 1.0", "https://creativecommons.org/publicdomain/zero/1.0/"}
    };
    static int index(String style) {
        for (int i = 0; i < STYLES.length; i++) if (STYLES[i].equals(style)) return i;
        return -1;
    }
}
