/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
// Modified 2026-11-05 by new-api-sub: adapted from web/src/features/home (hero, features, how-it-works) and the public header/footer into a shared public home
import { LANDING_PATH } from '@app/app-config'
import { Link } from '@tanstack/react-router'
import { ArrowRight, type LucideIcon } from 'lucide-react'
import { useEffect, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { AnimateInView } from '@/components/animate-in-view'
import { LanguageSwitcher } from '@/components/language-switcher'
import { ProjectAttribution } from '@/components/project-attribution'
import { ThemeSwitch } from '@/components/theme-switch'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { useSystemConfig } from '@/hooks/use-system-config'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

// Copy fields are English source text, translated with t() when rendered.
export type HomeContent = {
  badge: string
  title: string
  highlight: string
  description: string
  features: { icon: LucideIcon; title: string; description: string }[]
  steps: { title: string; description: string }[]
  /** Extra hero buttons, built by the app so links stay typed to its routes. */
  actions?: ReactNode
}

function Brand() {
  const { t } = useTranslation()
  const { systemName, logo, loading } = useSystemConfig()
  return (
    <div className='flex min-w-0 items-center gap-2.5'>
      {loading ? (
        <Skeleton className='size-7 shrink-0 rounded-lg' />
      ) : (
        <img
          src={logo}
          alt={t('Logo')}
          className='size-7 shrink-0 rounded-lg object-contain'
        />
      )}
      {loading ? (
        <Skeleton className='h-4 w-16' />
      ) : (
        <span className='max-w-48 truncate text-sm font-semibold tracking-tight'>
          {systemName}
        </span>
      )}
    </div>
  )
}

/** Sign in for visitors, Console for signed-in users. */
function PrimaryAction({ className }: { className: string }) {
  const { t } = useTranslation()
  const signedIn = useAuthStore((s) => !!s.auth.user)
  return (
    <Button
      size='sm'
      className={className}
      render={<Link to={signedIn ? LANDING_PATH : '/sign-in'} />}
    >
      {signedIn ? t('Console') : t('Sign in')}
      <ArrowRight className='ml-1.5 size-4 transition-transform duration-200 group-hover:translate-x-0.5' />
    </Button>
  )
}

function Header() {
  const [scrolled, setScrolled] = useState(false)

  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 20)
    onScroll()
    window.addEventListener('scroll', onScroll, { passive: true })
    return () => window.removeEventListener('scroll', onScroll)
  }, [])

  return (
    <header className='pointer-events-none fixed inset-x-0 top-0 z-50'>
      <div
        className={cn(
          'pointer-events-auto mx-auto transition-all duration-700 ease-[cubic-bezier(0.16,1,0.3,1)]',
          scrolled ? 'max-w-[52rem] px-3 pt-3' : 'max-w-7xl px-4 pt-0 md:px-6'
        )}
      >
        <nav
          className={cn(
            'flex items-center justify-between gap-2 transition-all duration-700 ease-[cubic-bezier(0.16,1,0.3,1)]',
            scrolled
              ? 'bg-background/60 ring-border/50 h-12 rounded-2xl pr-1.5 pl-4 shadow-[0_2px_16px_-6px_rgba(0,0,0,0.08),0_0_0_0.5px_rgba(0,0,0,0.02)] ring-[0.5px] backdrop-blur-2xl dark:shadow-[0_2px_16px_-6px_rgba(0,0,0,0.4)]'
              : 'h-16 px-2'
          )}
        >
          <Brand />
          <div className='flex shrink-0 items-center gap-1'>
            <LanguageSwitcher />
            <ThemeSwitch />
            <PrimaryAction className='group ml-1 h-8 rounded-lg px-3.5 text-xs font-medium' />
          </div>
        </nav>
      </div>
    </header>
  )
}

function Hero({ content }: { content: HomeContent }) {
  const { t } = useTranslation()
  return (
    <section className='relative z-10 overflow-hidden px-6 pt-32 pb-16 md:pt-40 md:pb-24'>
      <div
        aria-hidden
        className='pointer-events-none absolute inset-0 -z-10 opacity-25 dark:opacity-[0.12]'
        style={{
          background: [
            'radial-gradient(ellipse 60% 50% at 20% 20%, oklch(0.72 0.18 250 / 80%) 0%, transparent 70%)',
            'radial-gradient(ellipse 50% 40% at 80% 15%, oklch(0.65 0.15 200 / 60%) 0%, transparent 70%)',
            'radial-gradient(ellipse 40% 35% at 40% 80%, oklch(0.70 0.12 280 / 40%) 0%, transparent 70%)',
          ].join(', '),
        }}
      />
      <div
        aria-hidden
        className='absolute inset-0 -z-10 bg-[linear-gradient(to_right,var(--border)_1px,transparent_1px),linear-gradient(to_bottom,var(--border)_1px,transparent_1px)] [mask-image:radial-gradient(ellipse_60%_50%_at_50%_30%,black_20%,transparent_100%)] bg-[size:4rem_4rem] opacity-[0.08]'
      />

      <div className='mx-auto flex max-w-3xl flex-col items-center text-center'>
        <div
          className='landing-animate-fade-up mb-5 inline-flex items-center gap-1.5 rounded-full border border-blue-500/20 bg-blue-500/5 px-3 py-1.5 text-[11px] font-medium text-blue-600 opacity-0 shadow-xs dark:border-blue-400/20 dark:bg-blue-400/5 dark:text-blue-400'
          style={{ animationDelay: '0ms' }}
        >
          <span className='relative flex size-1.5'>
            <span className='absolute inline-flex h-full w-full animate-ping rounded-full bg-blue-400 opacity-75' />
            <span className='relative inline-flex size-1.5 rounded-full bg-blue-500 dark:bg-blue-400' />
          </span>
          <span>{t(content.badge)}</span>
        </div>

        <h1
          className='landing-animate-fade-up text-[clamp(2.25rem,4.5vw,3.25rem)] leading-[1.15] font-bold tracking-tight'
          style={{ animationDelay: '60ms' }}
        >
          {t(content.title)}
          <br />
          <span className='bg-gradient-to-r from-blue-400 via-violet-400 to-purple-500 bg-clip-text text-transparent'>
            {t(content.highlight)}
          </span>
        </h1>
        <p
          className='landing-animate-fade-up text-muted-foreground/80 mt-5 max-w-xl text-base leading-relaxed opacity-0 md:text-[15px]'
          style={{ animationDelay: '120ms' }}
        >
          {t(content.description)}
        </p>

        <div
          className='landing-animate-fade-up mt-8 flex flex-wrap justify-center gap-3 opacity-0'
          style={{ animationDelay: '180ms' }}
        >
          <PrimaryAction className='group h-11 rounded-lg px-5 text-sm font-medium' />
          {content.actions}
        </div>
      </div>
    </section>
  )
}

function Features({ features }: { features: HomeContent['features'] }) {
  const { t } = useTranslation()
  return (
    <section className='relative z-10 px-6 py-24 md:py-32'>
      <div className='mx-auto max-w-6xl'>
        <AnimateInView className='mb-16'>
          <h2 className='text-2xl leading-tight font-bold tracking-tight md:text-3xl'>
            {t('Features')}
          </h2>
        </AnimateInView>

        <div className='border-border/40 bg-border/40 grid gap-px overflow-hidden rounded-xl border md:grid-cols-3'>
          {features.map((f, i) => (
            <AnimateInView
              key={f.title}
              delay={i * 100}
              animation='scale-in'
              className={cn(
                'bg-background hover:bg-muted/20 p-7 transition-colors duration-300 md:p-8',
                // Bento layout: wide, narrow, narrow, wide.
                i % 3 === 0 && 'md:col-span-2'
              )}
            >
              <div className='mb-3 flex items-center gap-3'>
                <span className='border-border/40 bg-muted text-muted-foreground flex size-7 items-center justify-center rounded-md border text-[10px] font-semibold tabular-nums'>
                  {String(i + 1).padStart(2, '0')}
                </span>
                <h3 className='text-sm font-semibold'>{t(f.title)}</h3>
                <f.icon
                  className='text-muted-foreground ml-auto size-4 shrink-0'
                  strokeWidth={1.5}
                />
              </div>
              <p className='text-muted-foreground text-sm leading-relaxed'>
                {t(f.description)}
              </p>
            </AnimateInView>
          ))}
        </div>
      </div>
    </section>
  )
}

function Steps({ steps }: { steps: HomeContent['steps'] }) {
  const { t } = useTranslation()
  return (
    <section className='border-border/40 relative z-10 border-t px-6 py-24 md:py-32'>
      <div className='mx-auto max-w-6xl'>
        <AnimateInView className='mb-16 text-center md:mb-20'>
          <h2 className='text-2xl font-bold tracking-tight md:text-3xl'>
            {t('How it works')}
          </h2>
        </AnimateInView>

        <div className='grid gap-8 md:grid-cols-3 md:gap-12'>
          {steps.map((step, i) => (
            <AnimateInView
              key={step.title}
              delay={i * 150}
              className='flex flex-col items-center text-center'
            >
              <div className='text-muted-foreground border-border/50 bg-muted/30 mb-6 flex size-16 items-center justify-center rounded-2xl border text-xl font-semibold tabular-nums'>
                {i + 1}
              </div>
              <h3 className='mb-2 text-base font-semibold'>{t(step.title)}</h3>
              <p className='text-muted-foreground max-w-[240px] text-sm leading-relaxed'>
                {t(step.description)}
              </p>
            </AnimateInView>
          ))}
        </div>
      </div>
    </section>
  )
}

function Footer() {
  const { systemName } = useSystemConfig()
  return (
    <footer className='border-border/40 relative z-10 border-t'>
      <div className='mx-auto flex max-w-6xl flex-col items-center justify-between gap-4 px-6 py-8 sm:flex-row'>
        <Brand />
        <div className='flex flex-col items-center gap-1 text-center sm:items-end sm:text-right'>
          <span className='text-muted-foreground/60 text-xs'>
            &copy; {new Date().getFullYear()} {systemName}
          </span>
          <ProjectAttribution />
        </div>
      </div>
    </footer>
  )
}

/** Public shell: the home header and footer around any page. */
export function PublicPage({ children }: { children: ReactNode }) {
  return (
    <div className='bg-background text-foreground relative min-h-svh overflow-x-clip'>
      <Header />
      <main>{children}</main>
      <Footer />
    </div>
  )
}

export function HomePage(content: HomeContent) {
  return (
    <PublicPage>
      <Hero content={content} />
      <Features features={content.features} />
      <Steps steps={content.steps} />
    </PublicPage>
  )
}
