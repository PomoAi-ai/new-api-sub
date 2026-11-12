/*
Copyright (C) 2026 new-api-sub contributors

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
*/
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

const JSON_HEADER = `-H 'Content-Type: application/json'`
const AUTH_HEADER = `-H "Authorization: Bearer $TOKEN"`

/** [method and path, what it does, parameters] */
const ENDPOINTS: [string, string, string][] = [
  [
    'GET /api/channel/',
    'List your channels, newest first',
    'p, page_size, status, type, group',
  ],
  ['GET /api/channel/{id}', 'Get one channel', ''],
  [
    'GET /api/channel/types',
    'List the channel types and templates you can use',
    '',
  ],
  [
    'POST /api/channel/',
    'Create channels',
    'mode, multi_key_mode, template_id, channel',
  ],
  [
    'PUT /api/channel/',
    'Change the name or models of a channel',
    'id, name, models',
  ],
  [
    'POST /api/channel/{id}/status',
    'Enable (1) or disable (2) a channel',
    'status',
  ],
  [
    'GET /api/channel/test/{id}',
    'Test a channel',
    'model, endpoint_type, stream',
  ],
  [
    'GET /api/channel/update_balance/{id}',
    'Refresh the balance of a channel',
    '',
  ],
  ['DELETE /api/channel/{id}', 'Delete a channel', ''],
  [
    'POST /api/channel/multi_key/manage',
    'Manage the keys of a multi-key channel',
    'channel_id, action, ...',
  ],
  [
    'GET /api/log/self',
    'List usage logs',
    'start_timestamp, end_timestamp, channel, type, model_name, group, request_id, upstream_request_id, p, page_size',
  ],
  ['GET /api/log/self/stat', 'Get usage log totals', 'same as /api/log/self'],
  [
    'GET /api/data/self',
    'Get usage by model over time',
    'same as /api/log/self',
  ],
]

/** [action, parameters, what it does] */
const KEY_ACTIONS: [string, string, string][] = [
  [
    'get_key_status',
    'page, page_size, status',
    'List keys by position with their status. Key contents are never returned.',
  ],
  ['enable_key', 'key_index', 'Enable one key'],
  ['disable_key', 'key_index', 'Disable one key'],
  ['test_keys', 'key_indexes or key_index, model', 'Test keys with a model'],
  [
    'get_key_balance',
    'key_indexes or key_index',
    'Query key balances; all keys when none is given',
  ],
]

/** [status, meaning] */
const STATUS_CODES: [string, string][] = [
  ['200, success: true', 'Done.'],
  [
    '200, success: false',
    'The request reached the service but failed: bad parameters, an upstream refusal or an internal error. The reason is in message. These are not 5xx responses, so an HTTP client will not retry a creation on its own.',
  ],
  [
    '401',
    'The token is missing, invalid or expired, or the account is disabled.',
  ],
  ['403', 'The feature is not enabled, such as per-key test and balance.'],
  [
    '404',
    'The channel or key does not exist or is not yours, or the path does not exist.',
  ],
  [
    '409',
    'Try again later: a new channel is not in service yet, or the channel has a batch operation running.',
  ],
  [
    '429',
    'Rate limit reached. The Retry-After header says how many seconds to wait.',
  ],
]

const NOTES = [
  'Creation does not deduplicate and has no idempotency key: submitting the same key twice makes two channels.',
  'Do not blindly retry a creation that failed or timed out. List your channels first to see whether it went through. If message contains “结果未知” or “请联系管理员”, do not retry; ask your administrator to check.',
  'A new channel answers 409 to enabling, disabling, balance and deletion until it has synced. Wait a moment and call again; the channel exists, so do not create it again.',
  'All your tokens together may make 120 calls every 180 seconds.',
  'multi_to_single without multi_key_mode succeeds, but the channel then uses only its first enabled key. Pass random or polling to use all of them.',
  'Group, priority and weight come from the template; such fields in the request are ignored. base_url applies only when the template allows it. When a type has several templates, template_id is required. key can be up to 64 KB.',
  'PUT changes only name and models. A request with a non-empty key, base_url, key_mode, multi_key_mode or status fails as a whole; use the status endpoint for status.',
  'A channel with exists: false has been removed from service and only its record is left here. It shows as disabled; enabling, testing and balance answer 404, and DELETE removes the record.',
  'Log queries cover at most 92 days, and the last 92 days when no time is given. end_timestamp is inclusive. type is 2 (consumption), 5 (error) or 6 (refund); by default all three.',
]

function Section(props: { title: string; children: ReactNode }) {
  return (
    <section className='space-y-3'>
      <h3 className='text-base font-semibold'>{props.title}</h3>
      {props.children}
    </section>
  )
}

function Example(props: { title: string; code: string }) {
  return (
    <div className='space-y-1'>
      <p className='text-sm font-medium'>{props.title}</p>
      <div className='bg-muted/50 flex items-start gap-2 rounded-lg border p-3'>
        <pre className='min-w-0 flex-1 overflow-x-auto font-mono text-xs'>
          {props.code}
        </pre>
        <CopyButton value={props.code} className='size-7' />
      </div>
    </div>
  )
}

function DocTable(props: {
  head: string[]
  rows: string[][]
  mono?: number[]
}) {
  // phones get one block per row: a table there leaves the text columns a few pixels wide
  const stacked = (
    <div className='divide-y rounded-lg border sm:hidden'>
      {props.rows.map((row) => (
        <dl key={row[0]} className='space-y-2 p-3'>
          {row.map(
            (cell, index) =>
              cell && (
                <div key={props.head[index]}>
                  <dt className='text-muted-foreground text-xs'>
                    {props.head[index]}
                  </dt>
                  <dd
                    className={
                      props.mono?.includes(index)
                        ? 'font-mono text-xs break-all'
                        : 'text-sm'
                    }
                  >
                    {cell}
                  </dd>
                </div>
              )
          )}
        </dl>
      ))}
    </div>
  )
  return (
    <>
      {stacked}
      <div className='overflow-x-auto rounded-lg border max-sm:hidden'>
        <Table>
          <TableHeader>
            <TableRow>
              {props.head.map((title) => (
                <TableHead key={title}>{title}</TableHead>
              ))}
            </TableRow>
          </TableHeader>
          <TableBody>
            {props.rows.map((row) => (
              <TableRow key={row[0]}>
                {row.map((cell, index) => (
                  <TableCell
                    key={cell}
                    className={
                      props.mono?.includes(index)
                        ? 'font-mono text-xs'
                        : 'text-sm whitespace-normal'
                    }
                  >
                    {cell}
                  </TableCell>
                ))}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </>
  )
}

/** For portal users: nothing here may describe how the service is built. */
export function ApiDocs() {
  const { t } = useTranslation()
  const base = window.location.origin
  const channel = `${base}/api/channel/`
  const manage = `${base}/api/channel/multi_key/manage`

  return (
    <div className='max-w-4xl space-y-8'>
      <Section title={t('API Authentication')}>
        <p className='text-sm'>
          {t(
            'Create a token on the Token List tab. You verify your identity first, and the token is shown only once, so save it right away.'
          )}
        </p>
        <p className='text-sm'>
          {t(
            'Send it with every request in the Authorization header. Bearer is not case-sensitive, and the token alone also works.'
          )}
        </p>
        <Example
          title={t('Check that your token works')}
          code={`export TOKEN=ptl_your_token

curl -sS "${channel}?p=1&page_size=10" ${AUTH_HEADER}`}
        />
        <p className='text-sm'>
          {t(
            'A token can do what you can do on this site, and only with your own channels. It cannot manage tokens or change your password. Changing your password, or an administrator resetting it, revokes all your tokens. Disabling your account stops them. Signing out does not.'
          )}
        </p>
        <p className='text-sm'>
          {t('Responses have the form {success, message, data}.')}
        </p>
      </Section>

      <Section title={t('API Endpoints')}>
        <DocTable
          head={[t('API Endpoint'), t('Purpose'), t('Parameters')]}
          rows={ENDPOINTS.map(([path, purpose, params]) => [
            path,
            t(purpose),
            params === 'same as /api/log/self' ? t(params) : params,
          ])}
          mono={[0]}
        />
        <p className='text-sm'>
          {t(
            'Lists take p and page_size (default 10, at most 100) and return data = {items, total, page, page_size}. status is enabled or disabled. Creation returns data = {ids}. A channel never includes its keys.'
          )}
        </p>
        <p className='text-sm font-medium'>{t('Multi-key actions')}</p>
        <DocTable
          head={['action', t('Parameters'), t('Purpose')]}
          rows={KEY_ACTIONS.map(([action, params, purpose]) => [
            action,
            params,
            t(purpose),
          ])}
          mono={[0, 1]}
        />
      </Section>

      <Section title={t('Examples')}>
        <p className='text-sm'>
          {t(
            'type comes from GET /api/channel/types. Separate several keys with \\n.'
          )}
        </p>
        <Example
          title={t('Add one key: one channel')}
          code={`curl -sS "${channel}" ${AUTH_HEADER} ${JSON_HEADER} -d '{
  "mode": "single",
  "channel": {"type": 20, "name": "my-key", "key": "<key>", "models": "model-a,model-b"}
}'`}
        />
        <Example
          title={t('Add keys in batch: one channel per key')}
          code={`curl -sS "${channel}" ${AUTH_HEADER} ${JSON_HEADER} -d '{
  "mode": "batch",
  "channel": {"type": 20, "name": "my-keys", "key": "<key1>\\n<key2>", "models": "model-a,model-b"}
}'
# {"success":true,"message":"","data":{"ids":[101,102]}}`}
        />
        <Example
          title={t('Add several keys to one channel')}
          code={`curl -sS "${channel}" ${AUTH_HEADER} ${JSON_HEADER} -d '{
  "mode": "multi_to_single",
  "multi_key_mode": "polling",
  "channel": {"type": 20, "name": "my-pool", "key": "<key1>\\n<key2>", "models": "model-a,model-b"}
}'`}
        />
        <Example
          title={t('List disabled channels')}
          code={`curl -sS "${channel}?status=disabled&p=1&page_size=100" ${AUTH_HEADER}`}
        />
        <Example
          title={t('Change the name and models')}
          code={`curl -sS -X PUT "${channel}" ${AUTH_HEADER} ${JSON_HEADER} \\
  -d '{"id": 101, "name": "new-name", "models": "model-a"}'`}
        />
        <Example
          title={t('Disable a channel')}
          code={`curl -sS "${channel}101/status" ${AUTH_HEADER} ${JSON_HEADER} -d '{"status": 2}'`}
        />
        <Example
          title={t('Test a channel')}
          code={`curl -sS "${channel}test/101?model=model-a" ${AUTH_HEADER}
# {"success":true,"message":"","time":1.25}`}
        />
        <Example
          title={t('Refresh the balance')}
          code={`curl -sS "${channel}update_balance/101" ${AUTH_HEADER}`}
        />
        <Example
          title={t('Manage the keys of a multi-key channel')}
          code={`curl -sS "${manage}" ${AUTH_HEADER} ${JSON_HEADER} \\
  -d '{"channel_id": 101, "action": "get_key_status", "page": 1, "page_size": 50}'

curl -sS "${manage}" ${AUTH_HEADER} ${JSON_HEADER} \\
  -d '{"channel_id": 101, "action": "disable_key", "key_index": 3}'

curl -sS "${manage}" ${AUTH_HEADER} ${JSON_HEADER} \\
  -d '{"channel_id": 101, "action": "test_keys", "key_indexes": [0, 1], "model": "model-a"}'`}
        />
        <Example
          title={t('Query usage logs')}
          code={`curl -sS "${base}/api/log/self?start_timestamp=1760000000&end_timestamp=1760086400&p=1&page_size=50" ${AUTH_HEADER}`}
        />
      </Section>

      <Section title={t('Status codes')}>
        <DocTable
          head={[t('Status'), t('Meaning')]}
          rows={STATUS_CODES.map(([status, meaning]) => [status, t(meaning)])}
          mono={[0]}
        />
      </Section>

      <Section title={t('Notes')}>
        <ul className='list-disc space-y-2 ps-5 text-sm'>
          {NOTES.map((note) => (
            <li key={note}>{t(note)}</li>
          ))}
        </ul>
      </Section>
    </div>
  )
}
