export type NamedPerson = {
  id: string;
  nickname: string;
  username?: string;
  source_server_id?: string;
  source_server_name?: string;
};

export function userName(person: NamedPerson): string {
  const name = person.nickname.trim() || person.username?.trim() || person.id;
  const source =
    person.source_server_id &&
    (person.source_server_name?.trim() || person.source_server_id);
  return source ? `${name} · ${source}` : name;
}
