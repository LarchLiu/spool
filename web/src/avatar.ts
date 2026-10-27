import { Avatar, Style } from "@dicebear/core";
import clay from "@dicebear/styles/clay.json" with { type: "json" };
import pixelArt from "@dicebear/styles/pixel-art.json" with { type: "json" };
import croodles from "@dicebear/styles/croodles.json" with { type: "json" };
import lorelei from "@dicebear/styles/lorelei.json" with { type: "json" };
import notionists from "@dicebear/styles/notionists.json" with { type: "json" };
import toonHead from "@dicebear/styles/toon-head.json" with { type: "json" };
import voxelArt from "@dicebear/styles/voxel-art.json" with { type: "json" };
import thumbs from "@dicebear/styles/thumbs.json" with { type: "json" };

export const avatarStyles = [
  ["clay", "Clay"],
  ["pixel-art", "Pixel Art"],
  ["croodles", "Croodles"],
  ["lorelei", "Lorelei"],
  ["notionists", "Notionists"],
  ["toon-head", "Toon Head"],
  ["voxel-art", "Voxel Art"],
  ["thumbs", "Thumbs"],
] as const;
export type AvatarStyle = (typeof avatarStyles)[number][0];
export type AvatarConfig = { style: AvatarStyle; seed: string };
export type AvatarIdentity = {
  id: string;
  nickname: string;
  username?: string;
  avatar?: AvatarConfig;
};
export const defaultAvatar: AvatarConfig = {
  style: "clay",
  seed: "",
};

const definitions = {
  clay,
  "pixel-art": pixelArt,
  croodles,
  lorelei,
  notionists,
  "toon-head": toonHead,
  "voxel-art": voxelArt,
  thumbs,
};
const styles: Partial<Record<AvatarStyle, Style>> = {};

export function avatarAttribution(style: AvatarStyle) {
  return definitions[style].meta;
}

export function avatarName(person: AvatarIdentity): string {
  return person.nickname.trim() || person.username?.trim() || person.id;
}

export function avatarConfig(person: AvatarIdentity): AvatarConfig {
  const config = person.avatar;
  if (config && avatarStyles.some(([style]) => style === config.style))
    return config;
  // Also handle an older server response until its database has been upgraded.
  if (config && ["bottts-neutral", "shapes", "initials"].includes(config.style))
    return { style: defaultAvatar.style, seed: config.seed };
  return defaultAvatar;
}

export function avatarChoices(name: string, page: number): string[] {
  // Bound by Unicode code points so even four-byte characters fit the API limit.
  const prefix =
    [...name.replace(/\p{Cc}/gu, "")].slice(0, 24).join("") || "spool";
  return Array.from(
    { length: 12 },
    (_, index) => `${prefix}:${page * 12 + index + 1}`,
  );
}

export function avatarDataURI(person: AvatarIdentity): string {
  const config = avatarConfig(person);
  const options = {
    seed: config.seed || avatarName(person),
    size: 96,
    backgroundColor: ["dbe8d2", "f2d5c8", "e8dfb4", "cce2e5", "d8dded"],
  };
  // Validate and clone each bundled definition once, not for every thumbnail.
  const style = (styles[config.style] ??= new Style(definitions[config.style]));
  return new Avatar(style, options).toDataUri();
}
