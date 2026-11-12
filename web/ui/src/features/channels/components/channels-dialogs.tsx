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
// Modified 2026-11-05 by new-api-sub: only the create and edit drawers, the test, balance and key dialogs
import { useChannels } from './channels-provider'
import { BalanceQueryDialog } from './dialogs/balance-query-dialog'
import { ChannelKeysDialog } from './dialogs/channel-keys-dialog'
import { ChannelTestDialog } from './dialogs/channel-test-dialog'
import { ChannelMutateDrawer } from './drawers/channel-mutate-drawer'

export function ChannelsDialogs() {
  const { mode, open, setOpen, currentRow } = useChannels()

  return (
    <>
      {/* Channel Create Drawer */}
      <ChannelMutateDrawer
        variant='create'
        mode={mode}
        open={open === 'create-channel'}
        onOpenChange={(v) => !v && setOpen(null)}
      />

      {/* Channel Edit Drawer */}
      <ChannelMutateDrawer
        variant='edit'
        channel={currentRow}
        open={open === 'edit-channel'}
        onOpenChange={(v) => !v && setOpen(null)}
      />

      {/* Test Channel Dialog */}
      <ChannelTestDialog
        open={open === 'test-channel'}
        onOpenChange={(v) => !v && setOpen(null)}
      />

      {/* Balance Query Dialog */}
      <BalanceQueryDialog
        open={open === 'balance-query'}
        onOpenChange={(v) => !v && setOpen(null)}
      />

      {/* Keys of a multi-key channel */}
      <ChannelKeysDialog
        channel={currentRow}
        open={open === 'manage-keys'}
        onOpenChange={(v) => !v && setOpen(null)}
      />
    </>
  )
}
