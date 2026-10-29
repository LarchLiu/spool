import { Users } from "lucide-react";
import type { AvatarIdentity } from "./avatar";
import { AvatarStack } from "./user-avatar.js";

export function PeopleGroupAvatar({
  people = [],
}: {
  people?: AvatarIdentity[];
}) {
  return people.length ? (
    <span className="people-group-avatar" aria-hidden="true">
      <AvatarStack people={people} />
    </span>
  ) : (
    <span className="group-chat-avatar" aria-hidden="true">
      <Users size={18} />
    </span>
  );
}
