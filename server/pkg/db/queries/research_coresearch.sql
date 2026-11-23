-- name: ListResearchCoresearch :one
SELECT public.list_research_coresearch(@research_id::uuid)::jsonb AS chats;

-- name: GetResearchCoresearch :one
SELECT chat::jsonb AS chat
FROM public.get_research_coresearch(@research_id::uuid, @conversation_id::uuid) AS detail(chat);

-- name: GetResearchCoresearchArtifact :one
SELECT path::text, blob_key::text, size::bigint FROM public.get_research_coresearch_artifact(
  @research_id::uuid, @conversation_id::uuid, @artifact_id::uuid
);
