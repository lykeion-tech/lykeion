import { describe, expect, it } from 'vitest';
import { connectorInput, emptyConnector } from './settings/library/ConnectorConfigFields.vue';

describe('keyless command declaration', () => {
  it('sends the explicit declaration only when selected', () => {
    const fields = emptyConnector('local', 'command');
    fields.command = 'node';
    fields.keyless = true;
    expect(connectorInput(fields).config).toMatchObject({ command: 'node', keyless: true });
    fields.keyless = false;
    expect(connectorInput(fields).config).toMatchObject({ command: 'node', keyless: false });
  });
});
