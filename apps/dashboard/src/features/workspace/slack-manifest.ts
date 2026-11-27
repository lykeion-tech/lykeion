// The Slack app manifest the owner pastes into api.slack.com/apps (Create New App, From a manifest).
export const slackManifest = (name = 'Aristotle') => `display_information:
  name: ${name}
features:
  app_home:
    home_tab_enabled: false
    messages_tab_enabled: true
    messages_tab_read_only_enabled: false
  bot_user:
    display_name: ${name}
    always_online: true
  slash_commands:
    - command: /to-do
      description: Draft a study for To-do
      usage_hint: "[question]"
      should_escape: false
    - command: /new
      description: Start a new conversation
      should_escape: false
    - command: /clear
      description: Clear the current conversation
      should_escape: false
    - command: /help
      description: Show what ${name} can do
      should_escape: false
oauth_config:
  scopes:
    bot:
      - app_mentions:read
      - chat:write
      - im:history
      - im:read
      - im:write
      - reactions:write
      - users:read
      - users:read.email
      - commands
settings:
  event_subscriptions:
    bot_events:
      - app_mention
      - message.im
      - app_uninstalled
      - tokens_revoked
  interactivity:
    is_enabled: true
  org_deploy_enabled: false
  socket_mode_enabled: true
  token_rotation_enabled: false
`;
