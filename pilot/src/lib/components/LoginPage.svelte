<script lang="ts">
	import { Zap, Sparkles, Shield, GitBranch } from 'lucide-svelte';

	const features = [
		{ icon: Sparkles, title: 'AI-Powered', description: 'Claude handles your tasks autonomously' },
		{ icon: Shield, title: 'Private & Secure', description: 'Your data stays in your isolated project environment' },
		{ icon: GitBranch, title: 'Git Integration', description: 'Automatic branches and pull requests' },
	];
</script>

<div class="min-h-screen flex">
	<!-- Left side - Branding -->
	<div class="hidden lg:flex lg:w-[45%] bg-[oklch(0.97_0.01_55)] p-12 flex-col justify-between relative overflow-hidden">
		<!-- Subtle geometric accent -->
		<div class="absolute top-0 right-0 w-64 h-64 bg-[oklch(0.93_0.03_55)] rounded-full -translate-y-1/2 translate-x-1/3 opacity-60"></div>
		<div class="absolute bottom-0 left-0 w-96 h-96 bg-[oklch(0.94_0.02_55)] rounded-full translate-y-1/2 -translate-x-1/3 opacity-40"></div>

		<div class="relative">
			<div class="flex items-center gap-2.5">
				<div class="size-9 rounded-lg bg-[oklch(0.55_0.16_55)] flex items-center justify-center">
					<Zap class="size-5 text-white" />
				</div>
				<span class="font-bold text-xl tracking-tight">
					task<span class="text-[oklch(0.55_0.16_55)]">you</span>
				</span>
			</div>

			<div class="mt-20 max-w-md">
				<h1 class="text-[2.5rem] font-extrabold leading-[1.1] tracking-tight mb-5 text-[oklch(0.18_0.015_55)]">
					Your AI-powered<br />task execution engine
				</h1>
				<p class="text-base text-[oklch(0.45_0.02_55)] leading-relaxed max-w-sm">
					Delegate coding tasks to Claude and watch them get done.
					Review PRs, not implementations.
				</p>
			</div>

			<div class="mt-14 space-y-5">
				{#each features as feature}
					<div class="flex items-start gap-3">
						<div class="mt-0.5">
							<feature.icon class="size-4 text-[oklch(0.55_0.16_55)]" />
						</div>
						<div>
							<h3 class="font-semibold text-sm text-[oklch(0.22_0.015_55)]">{feature.title}</h3>
							<p class="text-sm text-[oklch(0.50_0.02_55)] mt-0.5">{feature.description}</p>
						</div>
					</div>
				{/each}
			</div>
		</div>

		<p class="text-xs text-[oklch(0.55_0.02_55)] relative">
			Powered by Anthropic's Claude &bull; Running on Cloudflare
		</p>
	</div>

	<!-- Right side - Login -->
	<div class="flex-1 flex items-center justify-center p-8 bg-background">
		<div class="w-full max-w-[340px]">
			<!-- Mobile logo -->
			<div class="lg:hidden flex items-center justify-center gap-2.5 mb-10">
				<div class="size-9 rounded-lg bg-[oklch(0.55_0.16_55)] flex items-center justify-center">
					<Zap class="size-5 text-white" />
				</div>
				<span class="font-bold text-xl tracking-tight">
					task<span class="text-[oklch(0.55_0.16_55)]">you</span>
				</span>
			</div>

			<div class="mb-8">
				<h2 class="text-xl font-bold tracking-tight mb-1">Sign in</h2>
				<p class="text-sm text-muted-foreground">
					Choose a method to get started
				</p>
			</div>

			<div class="space-y-2.5">
				<a
					href="/api/auth/google"
					class="w-full h-11 text-sm flex items-center justify-center gap-3 rounded-lg border border-border bg-background px-4 font-medium hover:bg-accent transition-colors"
				>
					<svg class="h-4 w-4" viewBox="0 0 24 24">
						<path fill="#4285F4" d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z" />
						<path fill="#34A853" d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" />
						<path fill="#FBBC05" d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z" />
						<path fill="#EA4335" d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" />
					</svg>
					Continue with Google
				</a>

				<a
					href="/api/auth/github"
					class="w-full h-11 text-sm flex items-center justify-center gap-3 rounded-lg border border-border bg-background px-4 font-medium hover:bg-accent transition-colors"
				>
					<svg class="h-4 w-4" viewBox="0 0 24 24" fill="currentColor">
						<path d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z" />
					</svg>
					Continue with GitHub
				</a>
			</div>

			<p class="text-[11px] text-muted-foreground/60 mt-8">
				By signing in, you agree to our
				<a href="#terms" class="underline hover:text-foreground transition-colors">Terms</a>
				and
				<a href="#privacy" class="underline hover:text-foreground transition-colors">Privacy Policy</a>
			</p>
		</div>
	</div>
</div>
