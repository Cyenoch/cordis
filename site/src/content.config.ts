import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { defineCollection } from 'astro:content'
import { glob, type Loader } from 'astro/loaders'
import { docsSchema } from '@astrojs/starlight/schema'
import { i18nLoader } from '@astrojs/starlight/loaders'
import { i18nSchema } from '@astrojs/starlight/schema'
import { z } from 'astro/zod'

const docsDirectory = new URL('../../docs/', import.meta.url)
const source = glob({ base: fileURLToPath(docsDirectory), pattern: '*.md' })

const loader: Loader = {
  name: 'cordis-docs',
  async load(context) {
    // Version queries are build inputs too, even when article text has not changed.
    context.store.clear()
    await source.load({
      ...context,
      async parseData(entry) {
        const markdown = await readFile(new URL(`${entry.id}.md`, docsDirectory), 'utf8')
        const title = entry.data.title ?? markdown.match(/^# (.+)$/m)?.[1]
        return context.parseData({
          ...entry,
          data: {
            ...entry.data,
            title,
            editUrl: entry.data.editUrl ?? `https://github.com/Cyenoch/cordis/edit/main/docs/${entry.id}.md`,
          },
        })
      },
    })
  },
}

export const collections = {
  docs: defineCollection({ loader, schema: docsSchema({
    extend: z.object({
      // StarlightPage also uses this schema for the non-document 404 page.
      // docs:sync requires both fields on every source article.
      codeCommit: z.string().regex(/^[a-f0-9]{40}$/).optional(),
      codePaths: z.array(z.string()).min(1).optional(),
    }),
  }) }),
  i18n: defineCollection({ loader: i18nLoader(), schema: i18nSchema() }),
}
