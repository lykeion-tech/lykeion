<script setup lang="ts">
const modifier =
  typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘' : 'Ctrl + ';
</script>

<template>
  <section aria-labelledby="shortcut-navigation">
    <h3 id="shortcut-navigation">Navigation and creation</h3>
    <dl class="shortcut-list">
      <div>
        <dt>Search</dt>
        <dd>
          <kbd>{{ modifier }}K</kbd>
        </dd>
      </div>
      <div>
        <dt>Toggle sidebar</dt>
        <dd>
          <kbd>{{ modifier }}B</kbd>
        </dd>
      </div>
      <div>
        <dt>New item</dt>
        <dd><kbd>C</kbd><small>Outside text fields and dialogs</small></dd>
      </div>
      <div>
        <dt>New workspace tab</dt>
        <dd><kbd>{{ modifier }}T</kbd><small>Browser shortcuts may take precedence</small></dd>
      </div>
      <div>
        <dt>Close workspace tab</dt>
        <dd><kbd>{{ modifier }}W</kbd><small>Browser shortcuts may take precedence</small></dd>
      </div>
      <div>
        <dt>Workspace tab history</dt>
        <dd><kbd>{{ modifier }}[</kbd><kbd>{{ modifier }}]</kbd></dd>
      </div>
      <div>
        <dt>Switch workspace tab</dt>
        <dd><kbd>Ctrl + Tab</kbd><small>Add Shift to go back; browser shortcuts may take precedence</small></dd>
      </div>
      <div>
        <dt>Aristotle model menu</dt>
        <dd><kbd>Ctrl + Shift + M</kbd><small>When Aristotle is available</small></dd>
      </div>
      <div>
        <dt>Toggle Aristotle</dt>
        <dd>
          <kbd>{{ modifier }}J</kbd><small>When Aristotle is available</small>
        </dd>
      </div>
    </dl>
  </section>
</template>

<style scoped>
h3 {
  margin-bottom: 12px;
}
.shortcut-list {
  margin: 0;
  background: var(--input);
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
}
.shortcut-list > div {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  align-items: center;
  padding: 16px;
}
.shortcut-list > div + div {
  border-top: 1px solid var(--border);
}
.shortcut-list dd {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: 0;
  text-align: right;
}
.shortcut-list small {
  color: var(--muted);
}
kbd {
  border: 1px solid var(--border);
  border-radius: 4px;
  padding: 3px 6px;
  font: inherit;
  white-space: nowrap;
}
</style>
