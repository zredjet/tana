# fsops の不変条件と、それを守るテスト

SPEC §2 の不変条件 I1〜I7 のそれぞれについて、それを破りうるコード経路と、それを防いでいるテストの対応をまとめる。
フェーズ4〜10の報告の対応表をまとめ直したもの（2026-09-24 時点。コミット `26ba576` 以降）。

- 経路は `internal/fsops` のファイル名と関数名で示す。テストも同じパッケージのもの。
- 「条件」の列: 共通 = Windows・macOS の CI で毎回実行。CROSSVOL・TRASH・EXFAT/FAT32・NUKE/SMALL = §18.2 の環境変数が必要
  （CI ではすべて設定している）。Windows・macOS = その OS だけ。
- CI で実行するのは Windows と macOS（`-race`）。Linux（ubuntu）では vet・プローブ・ごみ箱が使えないことのテストだけを実行する。
- 最後の節に、テストで守られていない経路を挙げる。

---

## I1 承認されていない上書きをしない

| 経路 | 防いでいるテスト | 条件 |
|---|---|---|
| 決定の検査と固定（`conflict.go` `decisionAllowed`・`Plan.Decide`、`execute.go` `Execute` の実行前の検査と決定のコピー） | TestDecide、TestExecutePreconditions（許されない決定で何もせず error） | 共通 |
| 未設定の決定を Skip として扱う（`copy.go` `copyTop`・`copyEntry`、`move.go` `moveItem`・`mergeEntry`） | TestCopyUnsetDecisionSkips、TestCopyMerge、TestMoveConflicts、TestMoveMerge | 共通 |
| 排他リネーム（`rename.go` `renameExclusive`・`renameExclusiveSysSame`、`rename_windows.go`・`rename_darwin.go`・`rename_linux.go` `renameExclusiveSys`） | TestRenameExclusive、TestRenameExclusiveHardLink（同じファイルへのハードリンクを上書きしない）、TestRenameExclusiveSameFile | 共通 |
| 排他リネームの代わりの手段（`rename_unix.go` `reserveThenRename`・`reserveThenRenameAt`） | TestReserveThenRename、TestRenameExclusiveOtherVolumes、TestCopyOtherVolumes、TestMoveMergeOtherVolumes | 共通・EXFAT/FAT32 |
| `Rename`（`rename.go` `Rename`。大文字小文字だけの変更の 2 段階の変更を含む） | TestRename、TestRenameCaseOnly、TestRenameCaseOnlyOtherVolumes、TestRenameReadOnly | 共通・EXFAT/FAT32 |
| コピーの最終名への排他リネーム・フォルダの作成・計画後に現れた衝突（`copy.go` `finalize`・`createResult`・`copyDir`） | TestCopyConflictAfterPlan、TestCopyConflictBeforeFinalRename、TestCopyMergeNewEntryAfterPlan、TestCopyOtherVolumes | 共通・CROSSVOL・EXFAT/FAT32 |
| 上書き・マージの直前の照合（`copy.go` `checkTarget`・`checkOverwrite`。移動でも使う） | TestCopyOverwriteTargetReplaced、TestCopyMergeTargetReplacedByLink、TestCopyTargetGone | 共通 |
| 読み取り専用・使用中の上書き先（`copy.go` `checkOverwrite`・`replaceResult`、`attr_*.go` `targetReadOnlySys`、`copy_windows.go` `inUseSys`） | TestCopyOverwriteReadOnly、TestCopyOverwriteLocked、TestMoveOverwriteReadOnly | 共通・Windows |
| 自動リネームの候補の確保（`copy.go` `autoRename`・`autoRenameName`） | TestCopyAutoRename（既存の `b (2).txt` を残す）、TestCopySelf、TestMoveConflicts | 共通 |
| シンボリックリンクを最終名に直接作る（`copy.go` `copySymlink`） | TestCopySymlinkConflictAfterPlan、TestCopyTopLevelLinks | 共通 |
| 同一ボリュームの移動のリネーム（`move.go` `rename`。トップレベルはパス、マージの中は開いたフォルダからの相対 `secDir.renameOut`） | TestMoveConflicts（計画後に現れた衝突）、TestMoveMerge、TestMoveMergeOtherVolumes | 共通・EXFAT/FAT32 |

## I2 移動元は最後に、コピーした分だけ消す

| 経路 | 防いでいるテスト | 条件 |
|---|---|---|
| 失敗・キャンセル・決定によらない Skip があれば移動元に手を付けない（`move.go` `copyThenRemove`） | TestMoveCrossVolumeFault、TestMoveCrossVolumeCancel、TestMoveCrossVolumeLinks（ジャンクション・FIFO） | CROSSVOL |
| 移動では必ず同期し、フォルダの同期の失敗で移動元を消さない（`copy.go` `sync`・`syncChanged`） | TestMoveSyncFailureKeepsSource | 共通 |
| コピーしたエントリの記録（`copy.go` `record`、`copyFile`・`copySymlink`・`copyDir`） | TestMoveCrossVolume、TestMoveCrossVolumeMergeSkip、TestMoveOtherVolumes | CROSSVOL・EXFAT/FAT32 |
| 記録との照合と、記録したものだけの削除（`remove.go` `removeRecorded`・`removeRecordedContents`・`recordEntry.matches`） | TestRemoveRecordedAll、TestRemoveRecordedKeepsChanged、TestMoveCrossVolumeAddedFile、TestMoveCrossVolumeEditedFile | 共通・CROSSVOL |
| 衝突の決定によるスキップで残したものの扱い（`remove.go` `onlyNames`） | TestMoveCrossVolumeMergeSkip、TestMoveCrossVolumeDetailsOrder | CROSSVOL |
| 移動元の削除の失敗・キャンセル（`remove.go` `remover`） | TestMoveCrossVolumeLocked、TestMoveCrossVolumeCancelRemoval、TestRemoveRecordedCancel | CROSSVOL・Windows |
| 読み取り専用の移動元（`secdir_windows.go` `removeSys` の属性の扱い） | TestMoveCrossVolumeReadOnly、TestRemoveRecordedReadOnly | CROSSVOL・共通 |
| ボリューム違いのエラーからの切り替え（`move.go` `moveItem`） | TestMoveRenameFallback | 共通 |

## I3 書きかけのファイルを最終名で残さない

| 経路 | 防いでいるテスト | 条件 |
|---|---|---|
| 一時名で書いてからリネームする（`copy.go` `writeTemp`・`createTemp`・`finalize`） | TestCopyTree、TestCopyWriteFailure | 共通 |
| キャンセルで一時ファイルを消す（`copy.go` `writeTemp` のバッファごとの確認、`finalize`） | TestCopyCancelMidFile、TestCopyCancelInFolder、TestMoveCrossVolumeCancel | 共通・CROSSVOL |
| 書き込みの失敗・容量不足で一時ファイルを消す（`copy.go` `writeTemp`、`removeTemp`） | TestCopyWriteFailure、TestCopyNoSpaceInjected、TestCopyNoSpaceCrossVolume | 共通・CROSSVOL |
| 検証の失敗で一時ファイルを消す（`copy.go` `verify`） | TestCopySourceChangedDuringCopy、TestCopyVerifyHash | 共通 |
| 最終名にできなかったときに一時ファイルを消す（読み取り専用にした一時ファイルを含む。`copy.go` `removeTemp`、`copy_windows.go` `clearReadOnlySys`） | TestCopyConflictBeforeFinalRename、TestCopyOverwriteTargetReplaced、TestCopyOverwriteLocked、TestCopyAutoRenameTooLong、TestCopyReadOnlyTempRemoved | 共通・Windows |
| コピー元を開けない場合は一時ファイルを作らない（`copy.go` `writeTemp`、`copy_*.go` `openSourceSys`） | TestCopyLockedSource | Windows |

## I4 リンクの先を操作しない

| 経路 | 防いでいるテスト | 条件 |
|---|---|---|
| 種類の判定（`entry*.go` `lstatEntry`、`entryTypeFromAttrs`） | TestLstatEntryLinks、TestLstatEntryJunction、TestEntryTypeFromAttrs | 共通・Windows |
| リンクを辿らない列挙（`walk*.go` `readDir`） | TestReadDirNotADir、TestReadDirJunction | 共通・Windows |
| 完全削除の走査で、開いたハンドルで確かめてから入る（`secdir_*.go` `openSecDir`、`remove.go` `enter`・`deleteContents`） | TestDeleteKeepsLinkTargets、TestDeleteDirReplacedByLinkBeforeEnter、TestDeleteTopLevelLinks、TestDeleteDirReplacedByFileBeforeRemove | 共通・Windows |
| 移動元の削除の走査（`remove.go` `removeRecorded`） | TestRemoveRecordedDirReplacedByLink、TestMoveCrossVolumeLinks | 共通・CROSSVOL |
| コピーでリンクに入らない（`copy.go` `copyEntry`・`copyDir`・`copySymlink`） | TestCopyTreeWithLinks、TestCopyDirReplacedByLink、TestCopyLinkSkip | 共通 |
| コピーのメタデータの設定がリンクを辿らない（`meta_*.go` `setMetaSys` の O_NOFOLLOW・リパースポイントを開かない、fileID の確認） | TestCopyTreeWithLinks（リンク先の更新日時・権限が変わらない）、TestCopyQuarantine | 共通・macOS |
| 同一ボリュームのマージ移動で、開いたハンドルで確かめてから入る（`move.go` `merge`） | TestMoveMergeDirReplacedByLink、TestMoveMergeLinks | 共通 |
| ごみ箱に入れる項目の大きさを数える走査（`trash_windows.go` `itemSize`）と、リンク自体だけを入れること | TestTrash（リンクを含むフォルダ、トップレベルのリンク・ジャンクション） | TRASH |

## I5 黙って完全削除しない

| 経路 | 防いでいるテスト | 条件 |
|---|---|---|
| 計画時の事前確認（`trash.go` `trashPrecheck`、`trash_windows.go` `trashAvailable`・`recycleCapacity`・`hasWin32UnsafeComponent`） | TestNewPlanTrashPrecheck、TestTrashPrecheckWindows、TestTrashPrecheckCapacity | 共通・Windows・NUKE/SMALL |
| 実行時にもう一度、今の大きさで確かめる（`trash.go` `trashItem`） | TestTrashExecuteRecheck、TestTrashWindowsUnavailable | Windows・SMALL |
| PreDeleteItem での中止（二つ目の防御。`trash_ifo_windows.go` の進捗通知） | TestTrashPreDeleteAbort | TRASH・NUKE |
| HRESULT の成否の判定（`trash_ifo_windows.go` `hresultFailed`） | TestHresultFailed | Windows |
| ごみ箱が使えないビルド（`trash_darwin_nocgo.go`、`trash_other.go`） | TestTrashUnavailable、TestNewPlanTrashPrecheck | ubuntu・macOS（`CGO_ENABLED=0`） |
| 成功を返しても元の場所に残っていれば失敗にする（`trash.go` `trashItem`） | TestTrash | TRASH |

## I6 ファイル名を変換しない

| 経路 | 防いでいるテスト | 条件 |
|---|---|---|
| 移動先・コピー先の名前はコピー元の名前をそのまま使う（`plan.go` `item`、`copy.go`、`move.go`） | TestCopyTree（日本語・絵文字・NFD）、TestMoveSameVolume | 共通 |
| Windows の `\\?\` 変換（`path_windows.go` `sysPath`・`userPath`） | TestSysPathWindows、TestUserPathWindows、TestRenameHelpersWin32UnsafeNames、TestReadDirWin32UnsafeNames、TestFileIDWin32UnsafeNames、TestLstatEntryWin32UnsafeNames | Windows |
| 自動リネームの候補（`copy.go` `autoRenameName`。切り詰めない） | TestAutoRenameName、TestCopyAutoRenameTooLong | 共通 |
| `Rename` の大文字小文字・正規化だけの変更（`rename.go` `Rename`） | TestRenameCaseOnly、TestRenameCaseOnlyOtherVolumes | 共通・EXFAT/FAT32 |
| ごみ箱に名前を変換せずに渡す（`trash_darwin_cgo.go` のファイルシステムの表現、`trash_windows.go` の正規化で変わる名前の拒否） | TestTrash（日本語の名前）、TestTrashWindowsUnavailable（`foo.` を入れようとしても `foo` が残る） | TRASH・Windows |
| リンク先の文字列を書き換えない（`copy.go` `copySymlink`） | TestCopyTreeWithLinks、TestCopyWindowsSymlinkKind | 共通・Windows |

## I7 キャンセル後・失敗後も I1〜I6 が成り立つ

キャンセル・失敗の経路ごとに、上の性質が保たれることを確かめているテスト。

| 経路 | 防いでいるテスト | 条件 |
|---|---|---|
| 実行前のキャンセル（`execute.go` `run`） | TestExecuteCanceledBeforeStart | 共通 |
| コピー中のキャンセル・失敗（I1・I3） | TestCopyCancelMidFile、TestCopyCancelInFolder、TestCopyWriteFailure、TestCopyNoSpaceInjected、TestCopySourceChangedDuringCopy | 共通 |
| 完全削除中のキャンセル・失敗（I4） | TestDeleteCancel、TestDeleteLocked、TestDeleteReadOnly | 共通・Windows |
| 移動中のキャンセル・失敗（I2・I4） | TestMoveCrossVolumeFault、TestMoveCrossVolumeCancel、TestMoveCrossVolumeCancelRemoval、TestMoveMergeCancel、TestMoveSyncFailureKeepsSource | 共通・CROSSVOL |
| リンクの作成の失敗（I1） | TestCopySymlinkCreateFails、TestCopySymlinkToFATVolumes | 共通・EXFAT/FAT32 |

---

## テストで守られていない経路

見つかったものを挙げる（まだ直していない）。

1. **Windows の、別名になりうる名前と長いパスのコピー・移動**（I6、§18.4「パス」の行）。
   末尾が `.`・空白の名前、予約名（`CON`）、260 文字を超えるパスは、名前の変更・完全削除・走査・fileID・ごみ箱の事前確認ではテストしているが、
   コピー・移動（同一ボリューム・ボリュームをまたぐ）のテストでは扱っていない。
2. **ハンドルを閉じた後のパスでのフォルダの削除**（I4）。
   完全削除のトップレベルのフォルダ、§13.3 のトップレベルのフォルダ、同一ボリュームのマージ移動で空になった移動元のフォルダは、
   ハンドルを閉じてからパスで `rmdir`・`RemoveDirectoryW` する（§13.1「フォルダ自体を削除する直前に閉じる」）。
   その間にジャンクションへ置き換えられると、Windows ではそのジャンクション自体を削除しうる（リンクの先には入らないが、計画にないエントリを消す）。
   この隙間を突く注入のテストはない。
3. **一時ファイルが最終名にする前に置き換えられた場合**（I1・I3）。
   `finalize` は一時ファイルの名前をリネームするので、書き終えた後に一時ファイルが別のものに置き換えられると、それを最終名にしてしまう。
   メタデータの設定と VerifyHash の読み直しは fileID を確かめるが、最終名へのリネームの直前には確かめていない。テストもない。
4. **照合の後にマージ先・上書き先を置き換えられた場合**（I1、書き込み先がリンクの先になる）。
   §7.3 の照合（`checkTarget`）はマージの開始時・上書きの直前に行うが、その後にマージ先をリンクへ置き換えられると、中身はリンクの先に書かれる。
   上書きでも、照合と置換リネームの間は `Lstat` による確認だけ（SPEC で許容）。照合より前の置き換えだけをテストしている。
5. **§8.4 の代わりの手段の残る危険**（I1）。名前を確保してから置き換えるまでの間に、確保した名前が消されて作り直された場合に上書きしうる（SPEC §8.4 に明記済み）。
6. **メタデータの設定の失敗の経路**。一時ファイル・作ったフォルダの fileID が一致しない場合の警告、Unix でコピー元のフォルダのメタデータを読めない場合の警告のテストがない
   （データは無事なので不変条件は破らない）。
7. **Windows のごみ箱の、事前確認を飛ばした最大サイズ超過**（I5）。確認ダイアログで止まることが V18 で分かっているため実行していない。
   また、フォルダの中身ごとに `PostDeleteItem` が届く場合に最初の失敗を覚える経路は、再現が難しくテストがない。
8. **Linux の実装が CI で実行されていない**。ubuntu ジョブでは fsops のテストのうちごみ箱が使えないことのテストだけを実行している。
   `renameat2` と代わりの手段（開いたフォルダからの相対を含む）、`attr_linux.go`、`volume_linux.go`、`meta_linux.go` の経路はテストされていない。
9. **macOS の exFAT の NFC の名前**（V17、§8.5 の制限事項）。NFC の名前で作られたエントリは NFD の名前で列挙され、その名前では削除できない。
   コピー・移動・完全削除で `KindNotFound` の失敗として報告するはずだが、テストはない。
10. **手元の macOS での FAT 系ボリュームのテスト**。手元の Mac では AppleDouble ファイル（`._名前`）が作られ、名前の一覧を比べる一部のテストが失敗する
    （TestRenameCaseOnlyOtherVolumes、TestRenameExclusiveOtherVolumes、TestMoveMergeOtherVolumes）。CI では作られず成功する。
