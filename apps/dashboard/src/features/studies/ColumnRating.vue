<script setup lang="ts">
import { ref } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { api } from '../../api/client';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import type { ColumnRating } from '../../api/types';
import { columnName } from '../../lib/studies';
import { notify } from '../../state/notice';

const props = defineProps<{ cardId: string; offer: ColumnRating }>();
const client = useQueryClient();
const busy = ref(false);

async function send(method: string, path: string, body?: unknown) {
  busy.value = true;
  try {
    await api(method, `/api/cards/${props.cardId}/ratings/${props.offer.column}${path}`, body);
  } catch (e) {
    notify(e instanceof Error ? e.message : String(e));
  } finally {
    busy.value = false;
    void client.invalidateQueries({ queryKey: keys.ratings(workspaceId.value, props.cardId) });
  }
}
const rate = (n: number | null) => send('PUT', '', { rating: n });
const dismiss = () => send('POST', '/dismiss');
</script>

<template>
  <section
    class="stage-box column-rating"
    :aria-label="'Rate the Team in ' + columnName(offer.column)"
  >
    <h4>
      <span class="stage-dot" :class="'status-' + offer.column" />{{ columnName(offer.column) }}
    </h4>
    <p class="rating-question">How did the Team do in this column? (optional)</p>
    <div class="rating-choices">
      <button
        v-for="n in 4"
        :key="n"
        class="button compact"
        :class="{ active: offer.rating === n }"
        :aria-pressed="offer.rating === n"
        :disabled="busy"
        @click="rate(n)"
      >
        {{ n }}
      </button>
      <button v-if="offer.rating === null" class="button compact" :disabled="busy" @click="dismiss">
        Dismiss
      </button>
      <button v-else class="button compact" :disabled="busy" @click="rate(null)">Clear</button>
    </div>
  </section>
</template>

<style scoped>
.column-rating h4 {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13.5px;
  margin-bottom: 6px;
}
.rating-question {
  margin: 6px 0 8px;
}
.rating-choices {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
</style>
