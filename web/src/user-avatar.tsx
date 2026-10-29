import { useMemo } from "react";
import { avatarDataURI, type AvatarIdentity } from "./avatar.js";

export function UserAvatar({
  person,
  size = 32,
}: {
  person: AvatarIdentity;
  size?: number;
}) {
  const src = useMemo(
    () => (person.bot ? "/logo.png" : avatarDataURI(person)),
    [
      person.id,
      person.bot,
      person.nickname,
      person.username,
      person.avatar?.style,
      person.avatar?.seed,
    ],
  );
  return (
    <img
      className="user-avatar"
      src={src}
      width={size}
      height={size}
      alt=""
      draggable={false}
    />
  );
}

export function AvatarStack({
  people,
  showTitles = true,
}: {
  people: AvatarIdentity[];
  showTitles?: boolean;
}) {
  if (!people.length) return null;
  const visible = people.slice(0, 3);
  const remaining = people.length - visible.length;
  return (
    <span className="avatar-stack" aria-hidden="true">
      {visible.map((person, index) => (
        <span
          key={person.id}
          className="avatar-stack-item"
          style={{ zIndex: visible.length - index }}
          title={showTitles ? person.nickname : undefined}
        >
          <UserAvatar person={person} size={24} />
        </span>
      ))}
      {remaining > 0 && (
        <span className="avatar-stack-item avatar-stack-more">
          +{remaining}
        </span>
      )}
    </span>
  );
}
