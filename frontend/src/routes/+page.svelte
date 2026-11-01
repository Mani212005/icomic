<div class="app-layout">
	<header class="navbar">
		<div class="brand">
			<span class="logo">🎨</span>
			<h1>Icomic</h1>
		</div>
		<div class="user-info">
			<span>Comic Generation Studio</span>
		</div>
	</header>

	<main class="main-container">
		<section class="hero">
			<h2>Welcome to Icomic</h2>
			<p>An AI-powered comic book generation studio.</p>
		</section>
	</main>
</div>

<style>
	:global(body) {
		margin: 0;
		padding: 0;
		font-family: system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
		background: #f1f5f9;
		color: #0f172a;
	}
	.app-layout {
		min-height: 100vh;
		display: flex;
		flex-direction: column;
	}
	.navbar {
		display: flex;
		justify-content: space-between;
		align-items: center;
		padding: 1rem 2rem;
		background: #ffffff;
		border-bottom: 1px solid #e2e8f0;
	}
	.brand {
		display: flex;
		align-items: center;
		gap: 0.5rem;
	}
	.logo {
		font-size: 1.5rem;
	}
	.navbar h1 {
		margin: 0;
		font-size: 1.25rem;
		font-weight: 700;
	}
	.main-container {
		flex: 1;
		max-width: 1000px;
		width: 100%;
		margin: 2rem auto;
		padding: 0 1rem;
		box-sizing: border-box;
	}
	.hero {
		background: #ffffff;
		border: 1px solid #e2e8f0;
		border-radius: 8px;
		padding: 2rem;
		text-align: center;
	}
	.hero h2 {
		margin-top: 0;
	}
</style>
