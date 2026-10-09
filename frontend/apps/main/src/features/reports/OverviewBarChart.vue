<template>
  <BarChart :data="data" index="status" :categories="priorities" :show-grid-line="true" :show-x-axis="true"
    :show-y-axis="true" type="grouped" :x-formatter="xFormatter" :y-formatter="yFormatter"   :rounded-corners="4"/>
</template>

<script setup>
import { BarChart } from '@shared-ui/components/ui/chart-bar'

const props = defineProps({
  data: {
    type: Array,
    required: true,
    default: () => []
  }
})

const xFormatter = (tick) => {
  return props.data[tick]?.status ?? ''
}

const yFormatter = (tick) => {  
  return Number.isInteger(tick) ? tick : ''
}

// encatch: we added an "Urgent" priority (inserted in the DB; there's no admin UI for priorities).
const priorities = ["Low", "Medium", "High", "Urgent"]
</script>
