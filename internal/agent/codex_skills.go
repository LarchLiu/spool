package agent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	chatmodel "spool/internal/model"
)

type codexSkill struct {
	command chatmodel.SlashCommand
	name    string
	path    string
}

func (p *agentProcess) codexSkills(ctx context.Context, directory string) ([]codexSkill, error) {
	response, err := p.codexRPC(ctx, "skills/list", map[string]any{"cwds": []string{directory}, "forceReload": true})
	if err != nil {
		return nil, fmt.Errorf("discover Codex skills: %w", err)
	}
	return codexSkillCatalog(response, directory), nil
}

// Keep native paths on the server. Discovery and execution share this mapping
// so a selected token always resolves to the same native name and path.
func codexSkillCatalog(response map[string]any, directory string) []codexSkill {
	var skills []codexSkill
	seen := map[string]bool{}
	entries, _ := response["data"].([]any)
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		if filepath.Clean(stringValue(entry["cwd"])) != filepath.Clean(directory) {
			continue
		}
		items, _ := entry["skills"].([]any)
		for _, raw := range items {
			item, _ := raw.(map[string]any)
			name, path := stringValue(item["name"]), stringValue(item["path"])
			key := name + "\x00" + path
			if item["enabled"] != true || !publicCommandName(name) || strings.ContainsAny(name, "`\"") || !filepath.IsAbs(path) || seen[key] {
				continue
			}
			seen[key] = true
			metadata, _ := item["interface"].(map[string]any)
			displayName := stringValue(metadata["displayName"])
			if displayName == "" {
				displayName = name
			}
			description := stringValue(metadata["shortDescription"])
			if description == "" {
				description = stringValue(item["shortDescription"])
			}
			if description == "" {
				description = stringValue(item["description"])
			}
			skills = append(skills, codexSkill{name: name, path: path, command: chatmodel.SlashCommand{
				Name: name, DisplayName: chatmodel.Clip(displayName, 256), Description: chatmodel.Clip(description, 2048),
				ArgumentHint: "[task]", Kind: "skill", Scope: stringValue(item["scope"]), PluginID: chatmodel.Clip(stringValue(item["pluginId"]), 256),
			}})
		}
	}
	sort.Slice(skills, func(i, j int) bool {
		if skills[i].name != skills[j].name {
			return skills[i].name < skills[j].name
		}
		return skills[i].path < skills[j].path
	})
	counts, reserved := map[string]int{}, map[string]bool{}
	for _, command := range codexCommands() {
		reserved[command.Name] = true
	}
	for _, skill := range skills {
		counts[skill.name]++
	}
	used := map[string]bool{}
	for name := range counts {
		used[name] = true
	}
	for i := range skills {
		skill := &skills[i]
		if counts[skill.name] == 1 && !reserved[skill.name] {
			continue
		}
		digest := sha256.Sum256([]byte(skill.path))
		base := fmt.Sprintf("skill:%s:%x", skill.name, digest[:6])
		token := base
		for suffix := 2; used[token] || reserved[token]; suffix++ {
			token = fmt.Sprintf("%s:%d", base, suffix)
		}
		used[token] = true
		skill.command.Name = token
	}
	return skills
}
