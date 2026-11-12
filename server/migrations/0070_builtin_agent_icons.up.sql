-- Built-in agents made before they were given icons get one from the same set
-- new ones draw from, so every built-in agent shows an animal.
UPDATE agent
SET avatar_url = 'emoji:' || (ARRAY['🦊','🦁','🐼','🐸','🐴','🐝','🐛','🦋','🐞','🕷️','🐢','🐍','🐙','🐬','🐯'])[1 + floor(random() * 15)::int]
WHERE laboratory = 'built-in' AND avatar_url = '';
