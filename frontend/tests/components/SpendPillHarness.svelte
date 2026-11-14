<script>
	import SpendPill from '../../src/lib/design/SpendPill.svelte';
	import { spendStore, spendView } from '../../src/lib/api/spend.js';
</script>

<SpendPill view={spendView($spendStore)} />
