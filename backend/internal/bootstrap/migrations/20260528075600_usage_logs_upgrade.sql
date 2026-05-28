-- description: Upgrade usage_logs table.

ALTER TABLE public.usage_logs ADD COLUMN IF NOT EXISTS image_size text NOT NULL DEFAULT '';

UPDATE public.usage_logs
SET usage_metadata = (
	CASE
		WHEN jsonb_typeof(usage_metadata) = 'object' THEN usage_metadata
		ELSE '{}'::jsonb
	END
) || jsonb_build_object('openai.image.size', btrim(image_size))
WHERE image_size IS NOT NULL
	AND btrim(image_size) <> ''
	AND (
		usage_metadata IS NULL
		OR jsonb_typeof(usage_metadata) <> 'object'
		OR COALESCE(btrim(usage_metadata->>'openai.image.size'), '') = ''
	);

ALTER TABLE public.usage_logs DROP COLUMN IF EXISTS image_size;
ALTER TABLE public.usage_logs DROP COLUMN IF EXISTS cache_creation_5m_tokens;
ALTER TABLE public.usage_logs DROP COLUMN IF EXISTS cache_creation_1h_tokens;
ALTER TABLE public.usage_logs DROP COLUMN IF EXISTS cache_creation_1h_price;
ALTER TABLE public.usage_logs DROP COLUMN IF EXISTS usage_attributes;
ALTER TABLE public.usage_logs DROP COLUMN IF EXISTS usage_metrics;
ALTER TABLE public.usage_logs DROP COLUMN IF EXISTS usage_cost_details;

CREATE INDEX CONCURRENTLY IF NOT EXISTS usage_log_created_at ON public.usage_logs (created_at);
CREATE INDEX CONCURRENTLY IF NOT EXISTS usage_log_user_id_snapshot ON public.usage_logs (user_id_snapshot);
