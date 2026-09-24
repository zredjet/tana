# fsops の不変条件と、それを守るテスト

SPEC §2 の不変条件 I1〜I7 のそれぞれについて、それを破りうるコード経路と、それを防いでいるテストの対応をまとめる。
フェーズ4〜10の報告の対応表をまとめ直したもの（2026-09-24 時点。コミット `26ba576` 以降）。

- 経路は `internal/fsops` のファイル名と関数名で示す。テストも同じパッケージのもの。
- 「条件」の列: 共通 = Windows・macOS の CI で毎回実行。CROSSVOL・TRASH・EXFAT/FAT32・NUKE/SMALL = §18.2 の環境変数が必要
  （CI ではすべて設定している）。Windows・macOS = その OS だけ。
- CI では、Windows・macOS（`-race`）・Linux（ubuntu。`-race`。ext4 と vfat）のすべてでテスト一式を実行する。Linux では exFAT を用意できないので、exFAT の条件のテストは Skip される。
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
| 移動先・コピー先の孤立した AppleDouble ファイル（`._名前`）を、`名前` を作るときに OS が消す・置き換える（macOS の exFAT・FAT32） | 受け入れる制限事項（SPEC §8.5）。OS の動作は TestV22 で記録 | macOS・EXFAT/FAT32 |

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
| 照合の後・削除の直前に書き換えられた移動元を消さない（Windows: `removeVerified` の削除するハンドルでの照合。Unix は受け入れる危険。SPEC §13.3） | TestRemoveRecordedFileEditedBeforeRemove | Windows |
| AppleDouble の付属（`._名前`）は項目として記録・削除せず、移動元の `名前` と一緒に OS が消す（`appledouble_darwin.go` `dropAppleDouble`。§15 の `com.apple.quarantine` は移動先に残る） | TestAppleDoubleQuarantineOtherVolumes（move across volumes・delete） | macOS・EXFAT/FAT32 |

## I3 書きかけのファイルを最終名で残さない

| 経路 | 防いでいるテスト | 条件 |
|---|---|---|
| 一時名で書いてからリネームする（`copy.go` `writeTemp`・`createTemp`・`finalize`） | TestCopyTree、TestCopyWriteFailure | 共通 |
| キャンセルで一時ファイルを消す（`copy.go` `writeTemp` のバッファごとの確認、`finalize`） | TestCopyCancelMidFile、TestCopyCancelInFolder、TestMoveCrossVolumeCancel | 共通・CROSSVOL |
| 書き込みの失敗・容量不足で一時ファイルを消す（`copy.go` `writeTemp`、`removeTemp`） | TestCopyWriteFailure、TestCopyNoSpaceInjected、TestCopyNoSpaceCrossVolume | 共通・CROSSVOL |
| 検証の失敗で一時ファイルを消す（`copy.go` `verify`） | TestCopySourceChangedDuringCopy、TestCopyVerifyHash | 共通 |
| 一時ファイルが置き換えられていれば最終名にせず、置き換えたものを消さない（`copy.go` `tempFile.check`・`tempFile.remove`・`finalize`） | TestCopyTempReplacedBeforeFinalRename | 共通 |
| 最終名にできなかったときに一時ファイルを消す（読み取り専用にした一時ファイルを含む。`copy.go` `removeTemp`、`copy_windows.go` `clearReadOnlySys`） | TestCopyConflictBeforeFinalRename、TestCopyOverwriteTargetReplaced、TestCopyOverwriteLocked、TestCopyAutoRenameTooLong、TestCopyReadOnlyTempRemoved | 共通・Windows |
| コピー元を開けない場合は一時ファイルを作らない（`copy.go` `writeTemp`、`copy_*.go` `openSourceSys`） | TestCopyLockedSource | Windows |

## I4 リンクの先を操作しない

| 経路 | 防いでいるテスト | 条件 |
|---|---|---|
| 種類の判定（`entry*.go` `lstatEntry`、`entryTypeFromAttrs`） | TestLstatEntryLinks、TestLstatEntryJunction、TestEntryTypeFromAttrs | 共通・Windows |
| リンクを辿らない列挙（`walk*.go` `readDir`） | TestReadDirNotADir、TestReadDirJunction | 共通・Windows |
| 完全削除の走査で、開いたハンドルで確かめてから入る（`secdir_*.go` `openSecDir`、`remove.go` `enter`・`deleteContents`） | TestDeleteKeepsLinkTargets、TestDeleteDirReplacedByLinkBeforeEnter、TestDeleteTopLevelLinks、TestDeleteDirReplacedByFileBeforeRemove | 共通・Windows |
| 移動元の削除の走査（`remove.go` `removeRecorded`） | TestRemoveRecordedDirReplacedByLink、TestMoveCrossVolumeLinks | 共通・CROSSVOL |
| ハンドルを閉じた後のフォルダの削除（Windows: `secdir_windows.go` `removeVerified` の確かめたハンドルでの削除、Unix: `rmdir`・`unlinkat(AT_REMOVEDIR)`） | TestDeleteDirReplacedByLinkBeforeRemove、TestRemoveRecordedDirReplacedByLinkBeforeRemove、TestMoveMergeDirReplacedByLinkBeforeRemove | 共通 |
| 確かめた後に置き換えられたファイルを消さない（Windows: `secdir_windows.go` `removeVerified`・`markDelete`。Unix の `unlink`・`unlinkat` は受け入れる危険。SPEC §13.2） | TestDeleteFileReplacedBeforeRemove、TestRemoveRecordedFileReplacedBeforeRemove（読み取り専用の属性も変えない） | Windows |
| コピーでリンクに入らない（`copy.go` `copyEntry`・`copyDir`・`copySymlink`） | TestCopyTreeWithLinks、TestCopyDirReplacedByLink、TestCopyLinkSkip | 共通 |
| コピーのメタデータの設定がリンクを辿らない（`meta_*.go` `setMetaIn` の O_NOFOLLOW・リパースポイントを開かない、fileID の確認） | TestCopyTreeWithLinks（リンク先の更新日時・権限が変わらない）、TestCopyQuarantine | 共通・macOS |
| メタデータを fsops が作ったもの以外に設定しない（`setMetaIn` の fileID の照合）、読めなければ警告にする（`copy.go` `copyDir`） | TestMetaTempReplaced、TestMetaCreatedDirReplaced、TestMetaSourceDirUnreadable | 共通 |
| 同一ボリュームのマージ移動で、開いたハンドルで確かめてから入る（`move.go` `merge`） | TestMoveMergeDirReplacedByLink、TestMoveMergeLinks | 共通 |
| 書き込み先のフォルダ（DestDir・作ったフォルダ・マージ先）を確かめて開き、中の操作をそのハンドルで行う（`secdir_*.go` `openDestRoot`・`openNewSecDir`・`renameBetween` など、`copy.go` `copyDir`、`move.go` `merge`） | TestCopyMergeDestReplacedAfterCheck、TestCopyCreatedDestReplacedAfterMkdir、TestMoveMergeDestReplacedAfterCheck | 共通 |
| ごみ箱に入れる項目の大きさを数える走査（`trash_windows.go` `itemSize`）と、リンク自体だけを入れること | TestTrash（リンクを含むフォルダ、トップレベルのリンク・ジャンクション） | TRASH |

## I5 黙って完全削除しない

| 経路 | 防いでいるテスト | 条件 |
|---|---|---|
| 計画時の事前確認（`trash.go` `trashPrecheck`、`trash_windows.go` `trashAvailable`・`recycleCapacity`・`hasWin32UnsafeComponent`） | TestNewPlanTrashPrecheck、TestTrashPrecheckWindows、TestTrashPrecheckCapacity | 共通・Windows・NUKE/SMALL |
| 実行時にもう一度、今の大きさで確かめる（`trash.go` `trashItem`） | TestTrashExecuteRecheck | Windows・SMALL |
| 計画時に使えない項目を、実行時に何もせず失敗にする（`execute.go` `run`、`trash.go` `trashItem`） | TestTrashWindowsUnavailable、TestTrashUnavailable | Windows・ubuntu・macOS（`CGO_ENABLED=0`） |
| ごみ箱へ移す操作が成功を返しても、元の場所に残っていれば失敗にする（`trash.go` `trashItem`。ごみ箱に入ったと報告しない） | TestTrashReportedButLeft（`trashCall` フックで、成功を返して何もしない呼び出しに差し替える） | Windows・macOS（cgo） |
| PreDeleteItem での中止（二つ目の防御。`trash_ifo_windows.go` `progressSink.preDelete`） | TestTrashPreDeleteAbort、TestProgressSink | TRASH・NUKE・Windows |
| 事前確認が見落とした最大サイズ超過の最後の防御（`FOF_WANTNUKEWARNING` の確認ダイアログ） | TestTrashOverCapacityBypass | TRASH・SMALL |
| フォルダの中身ごとの結果の扱い（`progressSink.postDelete`。最初のパスと最初の失敗） | TestProgressSink | Windows |
| HRESULT の成否の判定（`trash_ifo_windows.go` `hresultFailed`） | TestHresultFailed | Windows |
| ごみ箱が使えないビルド（`trash_darwin_nocgo.go`、`trash_other.go`） | TestTrashUnavailable、TestNewPlanTrashPrecheck | ubuntu・macOS（`CGO_ENABLED=0`） |

## I6 ファイル名を変換しない

| 経路 | 防いでいるテスト | 条件 |
|---|---|---|
| コピー先の名前はコピー元の名前をそのまま使う（`plan.go` `item`、`copy.go`） | TestCopyTree（日本語・絵文字・NFD） | 共通 |
| 移動先の名前は移動元の名前をそのまま使う（同一ボリュームの `move.go` `rename`・`mergeEntry`、ボリュームをまたぐ `copyThenRemove` のコピーと記録した名前での削除。日本語・絵文字・NFD の名前を、トップレベルの項目・フォルダの中身・マージ先の中身に置く） | TestMoveNamesSameVolume、TestMoveNamesCrossVolume | 共通・CROSSVOL |
| macOS の exFAT で、列挙が NFD の名前を返す NFC の名前のファイル（名前を変換して探し直さず、失敗として報告する。§8.5、V17） | TestNFCOnExFATCopy、TestNFCOnExFATDelete、TestNFCOnExFATMove | macOS・EXFAT |
| Windows の `\\?\` 変換（`path_windows.go` `sysPath`・`userPath`） | TestSysPathWindows、TestUserPathWindows、TestRenameHelpersWin32UnsafeNames、TestReadDirWin32UnsafeNames、TestFileIDWin32UnsafeNames、TestLstatEntryWin32UnsafeNames | Windows |
| 末尾が `.`・空白の名前、予約名を含むコピー・移動で、同名の別ファイル（`foo`）と取り違えない（`\\?\` 変換を通る `copy.go`・`move.go`・`remove.go` の各経路） | TestCopyWin32UnsafeNames、TestMoveWin32UnsafeNames、TestMoveCrossVolumeWin32UnsafeNames | 共通・CROSSVOL |
| 260 文字を超えるパスのコピー・移動（上書き・自動リネーム・マージ・移動元の削除を含む） | TestCopyLongPath、TestMoveLongPath、TestMoveCrossVolumeLongPath | 共通・CROSSVOL |
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

1. （解決済み）Windows の、別名になりうる名前と長いパスのコピー・移動。I6 の表の TestCopyWin32UnsafeNames などでテストした。
2. （解決済み）ハンドルを閉じた後のパスでのフォルダの削除。Windows では確かめたハンドルで削除するように直した（§13.2）。I4 の表の TestDeleteDirReplacedByLinkBeforeRemove などでテストした。
   ファイルも、`DeleteFileW` の代わりに確かめたハンドルで削除するように直した（TestDeleteFileReplacedBeforeRemove など。POSIX 形式の削除の扱いは V23）。
   §13.3 では、削除するハンドルで大きさ・更新日時も照合し直す（TestRemoveRecordedFileEditedBeforeRemove）。
   Unix の `unlink`・`unlinkat` は名前で消すので、確かめた後の置き換え・書き換えは防げない（開いたハンドルで削除する方法がない。SPEC §13.2 で受け入れる危険）。
3. （解決済み。ごく短い隙間は残る）一時ファイルが最終名にする前に置き換えられた場合。リネームの直前に一時ファイルの fileID・大きさを確かめ、
   置き換えられていれば最終名にせず、置き換えたものも消さないように直した（§10.1 の手順 7・8）。I3 の表の TestCopyTempReplacedBeforeFinalRename でテストした。
   確かめてからリネームするまでの間の、ごく短い隙間は残る（リネームは名前で行うため）。
4. （解決済み。上書き先のエントリ自体の隙間は残る）照合の後にマージ先・作ったフォルダを置き換えられた場合。書き込み先のフォルダも §13.1 の方法で開いて確かめ、
   中の操作をそのハンドルで行うように直した（§13.1、§10.2、§11.1）。I4 の表の TestCopyMergeDestReplacedAfterCheck などでテストした。
   上書き先のエントリ自体を、照合と置換リネームの間に置き換えられる、ごく短い隙間は残る（§7.3 の `Lstat` による照合として SPEC で許容）。
5. （直せない。受け入れた危険）§8.4 の代わりの手段の残る危険（I1・I3）。名前を確保してから置き換えるまでの間に、確保した名前が消されて作り直された場合に
   上書きしうる。また、その間にプロセスが強制終了すると、最終名に空のファイル・空のフォルダが残る（データは一時ファイル・移動元に残る）。
   この代わりの手段を使うのは実際には macOS の exFAT だけで、そこには代わりになる不可分な操作が 1 つもないことを V21 のプローブで確かめた（SPEC §8.4、§20 V21）。
   起きうるのは数回のシステムコールの間だけで、データを失いうるのはファイルの場合だけ（フォルダは空のものだけ）。
6. （解決済み）メタデータの設定の失敗の経路。一時ファイル・作ったフォルダが置き換えられていればメタデータを設定しないこと、
   コピー元のフォルダのメタデータを読めなければ警告にすることを、I4 の表の TestMetaTempReplaced などでテストした（不変条件は破らない）。
7. （解決済み）Windows のごみ箱の、事前確認を飛ばした最大サイズ超過。別プロセスで実行し、ファイル・フォルダとも確認ダイアログで止まり、
   完全に残ることを TestTrashOverCapacityBypass でテストした（フォルダも中止ではなくダイアログで止まることが分かった。SPEC §12.2、§20 V19）。
   フォルダの中身ごとに `PostDeleteItem` が届く場合の処理は、TestProgressSink で単体テストした。
8. （解決済み）Linux の実装が CI で実行されていない。ubuntu ジョブでテスト一式を実行するようにした（ext4 を CROSSVOL、vfat を FAT32 に使う）。
   これで、ext4 が削除したファイルの inode 番号をすぐ再利用するために、置き換えた一時ファイルを自分のものと取り違える不具合が見つかり、
   一時ファイルの照合を fileID・大きさ・更新日時で行うように直した（TestCopyTempReplacedBeforeFinalRename）。
   Linux では exFAT を用意できないので、Linux の exFAT、および `renameat2` が `EINVAL` を返す場合の代わりの手段の経路（ext4・vfat では使われない）はテストされない。
9. （解決済み）macOS の exFAT の NFC の名前（V17、§8.5 の制限事項）。コピー・完全削除・ボリュームをまたぐ移動・マージ移動で、データを失わず、
   移動元に残った項目を Done と報告しないことを TestNFCOnExFATCopy などでテストした（macOS・EXFAT。結果は SPEC §8.5）。
10. （解決済み）移動で名前をバイト単位でそのまま使うこと（I6、§18.4 の I6 の「移動」）。同一ボリュームの移動（リネーム・マージ）と
    ボリュームをまたぐ移動（新しいフォルダ・マージ・移動元の削除）を、日本語・絵文字・NFD の名前で TestMoveNamesSameVolume・TestMoveNamesCrossVolume でテストした。
11. （解決済み）ごみ箱へ移す操作が成功を返したのに元の場所に残っている場合を失敗にする経路（`trash.go` `trashItem`）。ごみ箱へ移す呼び出しを
    テスト用フック（`trashCall`）で差し替え、TestTrashReportedButLeft で `OutcomeFailed`（`KindUnknown`）になり、項目に手を付けないことをテストした。
12. （解決済み）手元の macOS での FAT 系ボリュームのテスト。手元では OS がテストの作るファイルに拡張属性（`com.apple.provenance`）を付け、AppleDouble ファイル（`._名前`）が
    できていた。調べると、fsops が `._名前` を独立した項目として扱うため、同一ボリュームのマージ移動で §15 の `com.apple.quarantine` が失われる不具合が見つかった（V22）。
    macOS の exFAT・FAT32 では `名前` と並ぶ `._名前` を付属として列挙から除くようにし（SPEC §8.5）、TestAppleDoubleQuarantineOtherVolumes・TestAppleDoubleNamesOrdinary でテストした。
    `testfs.ListNames` も同じ見方で付属を除くので、手元でも TestRenameCaseOnlyOtherVolumes などが成功する。
