<script setup lang="ts">
import Modal from '../../../../ui/Modal.vue';
import { discardDialog, resolveDiscard } from '../../../../state/resource-draft';
</script>

<template>
  <Modal v-if="discardDialog" title="Discard unsaved changes?" @close="resolveDiscard(false)">
    <section class="discard-draft">
      <h2>Discard unsaved changes?</h2>
      <p>Your changes have not been saved. Discard them to continue?</p>
      <footer>
        <button class="button" autofocus @click="resolveDiscard(false)">Keep editing</button>
        <button class="button danger" @click="resolveDiscard(true)">Discard changes</button>
      </footer>
    </section>
  </Modal>
</template>

<style scoped>
.discard-draft {
  padding: 24px;
}
h2 {
  font-size: 18px;
  line-height: 24px;
}
p {
  margin: 12px 0 24px;
  font-size: 14px;
  line-height: 22px;
  color: var(--muted);
}
footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
</style>
