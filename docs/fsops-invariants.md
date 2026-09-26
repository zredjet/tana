# fsops の不変条件と、それを守るテスト

SPEC §2 の不変条件 I1〜I7 のそれぞれについて、それを破りうるコード経路と、それを防いでいるテストの対応をまとめる。
フェーズ4〜10の報告の対応表をまとめ直したもの（2026-09-24 時点。コミット `26ba576` 以降）。2026-09-25 の不変条件の監査で見直した
（防御を 1 か所ずつ外してテストが失敗するかを確かめ、確かめていないテストを表から外し、守られていなかった経路にテストを足した）。

- 経路は `internal/fsops` のファイル名と関数名で示す。テストも同じパッケージのもの（`TestV*` のプローブは `internal/fsops/internal/probe`）。
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
| 排他リネーム（`rename.go` `renameExclusiveSysSame`、`rename_windows.go`・`rename_darwin.go`・`rename_linux.go` `renameExclusiveSys`・`renameAtExclusiveSys`。`rename.go` の `renameExclusive`・`renameReplace` はテストからだけ使う） | TestRenameExclusive、TestRenameExclusiveHardLink（同じファイルへのハードリンクを上書きしない）、TestRenameExclusiveSameFile | 共通 |
| 排他リネームの代わりの手段（`rename_unix.go` `reserveThenRename`・`reserveThenRenameAt`） | TestReserveThenRename、TestRenameExclusiveOtherVolumes、TestCopyOtherVolumes、TestMoveMergeOtherVolumes | 共通・EXFAT/FAT32 |
| フォルダの作成（`mkdir.go` `Mkdir`。OS の不可分な作成で、同じ名前があれば失敗する。フェーズ17） | TestMkdirExisting（ファイル・フォルダ・リンク・切れたリンクを変えずに KindExist）、TestMkdirFoldedNames（大文字小文字・正規化だけが違う名前） | 共通 |
| `Rename`（`rename.go` `Rename`・`renameWith`。大文字小文字だけの変更の 2 段階の変更を含む） | TestRename、TestRenameExclusive、TestRenameTwoStepSecondFails・TestRenameTwoStepRollbackFails（2 回目の失敗と元に戻す段） | 共通 |
| コピーの最終名への排他リネーム・フォルダの作成・計画後に現れた衝突（`copy.go` `finalize`・`createResult`・`copyDir`） | TestCopyConflictAfterPlan、TestCopyConflictBeforeFinalRename、TestCopyMergeNewEntryAfterPlan、TestCopyTempReplacedBeforeFinalRename、TestCopySkipIgnoresSourceChange、TestCopyOtherVolumes | 共通・CROSSVOL・EXFAT/FAT32 |
| 上書き・マージの直前の照合（`copy.go` `checkTarget`・`checkOverwrite`・`overwriteOnce`。移動でも使う） | TestCopyOverwriteTargetReplaced、TestMoveOverwriteTargetReplaced、TestOverwriteTargetReplacedSameSize（同じ大きさ・更新日時の別のファイル。fileID の照合）、TestCopyMergeTargetReplacedByLink、TestMergeTargetReplacedByDir（別の実フォルダ）、TestCopyTargetGone、TestMoveOverwriteTargetGone | 共通 |
| 読み取り専用・使用中の上書き先（`copy.go` `checkOverwrite`・`replaceResult`、`attr_*.go` `targetReadOnlySys`、`copy_windows.go` `inUseSys`） | TestCopyOverwriteReadOnly、TestCopyOverwriteLocked、TestMoveOverwriteReadOnly | 共通・Windows |
| 自動リネームの候補の確保（`copy.go` `autoRename`・`autoRenameName`） | TestCopyAutoRename（既存の `b (2).txt` を残す）、TestMoveAutoRenameExistingCandidate（計画後に現れた候補も残す）、TestCopySelf | 共通 |
| シンボリックリンクを最終名に直接作る（`copy.go` `copySymlink`） | TestCopySymlinkConflictAfterPlan、TestCopyTopLevelLinks | 共通 |
| 同一ボリュームの移動のリネーム（`move.go` `rename`。トップレベルはパス、マージの中は開いたフォルダからの相対 `renameBetween`） | TestMoveConflicts（計画後に現れた衝突）、TestMoveMerge、TestMoveWin32UnsafeNames、TestMoveMergeOtherVolumes | 共通・EXFAT/FAT32 |
| 移動先・コピー先の孤立した AppleDouble ファイル（`._名前`）を、`名前` を作るときに OS が消す・置き換える（macOS の exFAT・FAT32） | 受け入れる制限事項（SPEC §8.5）。OS の動作は TestV22 で記録 | macOS・EXFAT/FAT32 |
| 使用中の一時的な失敗のやり直し（SPEC §17.1。`lockretry.go` `lockRetrier`、`copy.go` `finalize`・`overwriteOnce`、`move.go` `rename`、`rename.go` `renameWith`）。やり直すたびに一時ファイルの `check` と上書き先の照合からやり直す | TestLockRetryConflictDuringWait（待つ間に現れた衝突）、TestLockRetryOverwriteChangedDuringWait（待つ間に変わった上書き先）。TestLockRetryOverwrite・TestLockRetryMove・TestLockRetryRename はやり直して完了することだけを確かめる | 共通 |

## I2 移動元は最後に、コピーした分だけ消す

| 経路 | 防いでいるテスト | 条件 |
|---|---|---|
| 失敗・キャンセル・決定によらない Skip があれば移動元に手を付けない（`move.go` `copyThenRemove`） | TestMoveCrossVolumeFault、TestMoveCrossVolumeCancel、TestMoveCrossVolumeLinks（ジャンクション・FIFO） | CROSSVOL |
| 移動では必ず同期し、同期の失敗で移動元を消さない（`copy.go` `sync`・`syncDir`、`writeTemp` のファイルの同期） | TestMoveSyncFailureKeepsSource（フォルダ）、TestFileSyncFailure（ファイル。`beforeSyncFile` フック） | 共通・CROSSVOL |
| コピーしたエントリの記録（`copy.go` `record`、`copyFile`・`copySymlink`・`copyDir`） | TestMoveCrossVolume、TestMoveCrossVolumeMergeSkip、TestMoveOtherVolumes | CROSSVOL・EXFAT/FAT32 |
| 記録との照合と、記録したものだけの削除（`remove.go` `removeRecorded`・`removeRecordedContents`・`recordEntry.matches`） | TestRemoveRecordedAll、TestRemoveRecordedKeepsChanged（大きさの変化・種類の変化）、TestRemoveRecordedKeepsSameSizeReplacement（同じ大きさ・更新日時の別のファイル。fileID の照合）、TestMoveCrossVolumeAddedFile、TestMoveCrossVolumeEditedFile | 共通・CROSSVOL |
| 衝突の決定によるスキップで残したものの扱い（`remove.go` `onlyNames`） | TestMoveCrossVolumeMergeSkip、TestMoveCrossVolumeDetailsOrder | CROSSVOL |
| 移動元の削除の失敗・キャンセル（`remove.go` `remover`） | TestMoveCrossVolumeLocked、TestMoveCrossVolumeCancelRemoval、TestRemoveRecordedCancel、TestLockRetryCancelSourceRemoval（やり直しの待ちの間のキャンセル） | CROSSVOL・Windows |
| 読み取り専用の移動元（`secdir_windows.go` `removeVerified` の属性の扱い） | TestMoveCrossVolumeReadOnly、TestRemoveRecordedReadOnly（Windows・macOS） | CROSSVOL・Windows・macOS |
| ボリューム違いのエラーからの切り替え（`move.go` `moveItem`） | TestMoveRenameFallback | 共通 |
| 照合の後・削除の直前に書き換えられた移動元を消さない（Windows: `removeVerified` の削除するハンドルでの照合。Unix は受け入れる危険。SPEC §13.3） | TestRemoveRecordedFileEditedBeforeRemove | Windows |
| AppleDouble の付属（`._名前`）は項目として記録・削除せず、移動元の `名前` と一緒に OS が消す（`appledouble_darwin.go` `dropAppleDouble`。§15 の `com.apple.quarantine` は移動先に残る） | TestAppleDoubleQuarantineOtherVolumes（move across volumes・delete） | macOS・EXFAT/FAT32 |
| 移動元の削除の、使用中の間のやり直し（`remove.go` `removeWithRetry`） | TestLockRetryMoveCrossVolume（完了することだけ。やり直しで照合し直すのは Windows の `removeVerified`） | CROSSVOL |
| コピー先の上限を超えるファイルのコピーの失敗で、移動元に手を付けない（`copy.go` `writeTemp` の §10.6 の確認） | TestMoveFileTooLarge、TestFileTooLargeFAT32（OpMove） | CROSSVOL・FAT32 |

## I3 書きかけのファイルを最終名で残さない

| 経路 | 防いでいるテスト | 条件 |
|---|---|---|
| 一時名で書いてからリネームする（`copy.go` `writeTemp`・`createTemp`・`finalize`） | TestCopyTree、TestCopyWriteFailure | 共通 |
| フォルダの作成（`mkdir.go` `Mkdir`）は OS の不可分な作成 1 回で、途中の状態を作らない。失敗したら何も作らない | TestMkdirInvalidName（何も作らない）、TestMkdirParent | 共通 |
| キャンセルで一時ファイルを消す（`copy.go` `writeTemp` のバッファごとの確認、`finalize`） | TestCopyCancelMidFile、TestCopyCancelInFolder、TestMoveCrossVolumeCancel | 共通・CROSSVOL |
| 書き込みの失敗・容量不足で一時ファイルを消す（`copy.go` `writeTemp`、`lockRetrier.removeTemp`） | TestCopyWriteFailure、TestCopyNoSpaceInjected、TestCopyNoSpaceCrossVolume | 共通・CROSSVOL |
| 検証の失敗で一時ファイルを消す。fileID を記録した後は、一時ファイルのままの場合だけ消す（`copy.go` `verify`、`writeTemp` の `recorded`、§10.1 の手順 8） | TestCopySourceChangedDuringCopy、TestCopyVerifyHash、TestTempReplacedBeforeVerify（置き換えたものを消さない） | 共通 |
| 一時ファイルが置き換えられていれば最終名にせず、置き換えたものを消さない（`copy.go` `tempFile.check`・`tempFile.remove`・`finalize`） | TestCopyTempReplacedBeforeFinalRename | 共通 |
| 最終名にできなかったときに一時ファイルを消す（読み取り専用にした一時ファイルを含む。`copy.go` `tempFile.remove`、`copy_windows.go` `clearReadOnlySys`） | TestCopyConflictBeforeFinalRename、TestCopyOverwriteTargetReplaced、TestCopyOverwriteLocked、TestCopyAutoRenameTooLong、TestCopyReadOnlyTempRemoved | 共通・Windows |
| コピー元を開けない場合は一時ファイルを作らない（`copy.go` `writeTemp`、`copy_*.go` `openSourceSys`） | TestCopyLockedSource | Windows |
| 一時ファイルの削除の、使用中の間のやり直し。キャンセルの後もやり直す（`copy.go` `tempFile.remove`・`lockRetrier.removeTemp`。SPEC §17.1） | TestLockRetryCopyExhausted、TestLockRetryCopyCancel（`tempFile.remove`）、TestLockRetryCancelTempCleanup（`lockRetrier.removeTemp`） | 共通 |
| コピー先の上限を超えるファイルは一時ファイルを作らない。コピー中に上限を超えたら一時ファイルを消す（`copy.go` `writeTemp`、§10.6） | TestCopyFileTooLarge、TestCopyFileTooLargeGrows、TestFileTooLargeFAT32 | 共通・FAT32 |
| 同期の失敗で一時ファイルを消す（`copy.go` `writeTemp`） | TestFileSyncFailure | 共通 |

## I4 リンクの先を操作しない

| 経路 | 防いでいるテスト | 条件 |
|---|---|---|
| 種類の判定（`entry*.go` `lstatEntry`、`entryTypeFromAttrs`） | TestLstatEntryLinks、TestLstatEntryJunction、TestEntryTypeFromAttrs | 共通・Windows |
| リンクを辿らない列挙（`walk*.go` `readDir`） | TestReadDirNotADir、TestReadDirJunction | 共通・Windows |
| 一覧のための調べ・列挙（`list.go` `Lstat`・`ReadDir`・`Readlink`、`walk*.go` `readDirFollowSys`。フェーズ17）。渡されたフォルダ自体のリンクだけを辿り（利用者が入った場合）、中のエントリは辿らない。fsops の中の走査の `readDir` は今までどおり入らない | TestReadDirEntersLinkedFolder（中のリンクは TypeSymlink、内部の readDir はリンクに入らない）、TestReadDirPublic、TestListJunction | 共通・Windows |
| 完全削除の走査で、開いたハンドルで確かめてから入る（`secdir_*.go` `openSecDir`、`remove.go` `enter`・`deleteContents`） | TestDeleteKeepsLinkTargets、TestDeleteDirReplacedByLinkBeforeEnter、TestDeleteTopLevelLinks、TestDeleteDirReplacedByFileBeforeRemove | 共通・Windows |
| 移動元の削除の走査（`remove.go` `removeRecorded`） | TestRemoveRecordedDirReplacedByLink、TestMoveCrossVolumeLinks | 共通・CROSSVOL |
| ハンドルを閉じた後のフォルダの削除（Windows: `secdir_windows.go` `removeVerified` の確かめたハンドルでの削除、Unix: `rmdir`・`unlinkat(AT_REMOVEDIR)`） | TestDeleteDirReplacedByLinkBeforeRemove、TestRemoveRecordedDirReplacedByLinkBeforeRemove、TestMoveMergeDirReplacedByLinkBeforeRemove | 共通 |
| 確かめた後に置き換えられたファイルを消さない（Windows: `secdir_windows.go` `removeVerified`・`markDelete`。Unix の `unlink`・`unlinkat` は受け入れる危険。SPEC §13.2） | TestDeleteFileReplacedBeforeRemove、TestRemoveRecordedFileReplacedBeforeRemove（読み取り専用の属性も変えない） | Windows |
| コピーでリンクに入らない（`copy.go` `copyEntry`・`copyDir`・`copySymlink`） | TestCopyTreeWithLinks、TestCopyDirReplacedByLink、TestCopyLinkSkip | 共通 |
| コピーのメタデータの設定がリンクを辿らない（`meta_*.go` `setMetaIn` の O_NOFOLLOW・リパースポイントを開かない、fileID の確認。Windows の Zone.Identifier は、名前の変更を許さずに開いたハンドルで照合してから書く） | TestMetaTempReplaced（fileID の照合）、TestCopyZoneIdentifierLinkSwap（照合の後の名前の置き換え）、TestCopyQuarantine | 共通・Windows・macOS |
| メタデータを fsops が作ったもの以外に設定しない（`setMetaIn` の fileID の照合）、読めなければ警告にする（`copy.go` `copyDir`） | TestMetaTempReplaced、TestMetaCreatedDirReplaced、TestMetaSourceDirUnreadable | 共通 |
| 同一ボリュームのマージ移動で、開いたハンドルで確かめてから入る（`move.go` `merge`） | TestMoveMergeDirReplacedByLink、TestMoveMergeLinks | 共通 |
| 書き込み先のフォルダ（DestDir・作ったフォルダ・マージ先）を確かめて開き、中の操作をそのハンドルで行う（`secdir_*.go` `openDestRoot`・`openNewSecDir`・`renameBetween` など、`copy.go` `copyDir`、`move.go` `merge`） | TestCopyMergeDestReplacedAfterCheck、TestCopyCreatedDestReplacedAfterMkdir、TestMoveMergeDestReplacedAfterCheck、TestDestDirReplacedAfterPlan（DestDir の fileID の照合） | 共通 |
| ごみ箱に入れる項目の大きさを数える走査（`trash_windows.go` `itemSize`）と、リンク自体だけを入れること | TestTrash（リンクを含むフォルダ、トップレベルのリンク・ジャンクション） | TRASH |
| 削除の、使用中の間のやり直しで §13.2 の確認もやり直す（`remove.go` `removeWithRetry`、Windows は `removeVerified` の一連） | TestLockRetryDeleteLinkSwapDuringWait（待つ間にリンクへ置き換えてもリンク先が残る。削除はどの OS でもリンク自体にしか作用しないので、確認のやり直しは判別しない） | 共通 |
| 削除で、マウントポイント（別のボリューム）に入らない（`fileid.go` `onOtherVolume`・`mountPoint`、`remove.go` `deleteContents`・`removeRecordedContents`・`deleteItem`、`plan.go` `item`・`walk`。§13.1） | TestDeleteMountPoint（テストの中で `hdiutil` でマウントする）、TestOnOtherVolume | macOS・共通 |

## I5 黙って完全削除しない

| 経路 | 防いでいるテスト | 条件 |
|---|---|---|
| 計画時の事前確認（`trash.go` `trashPrecheck`、`trash_windows.go` `trashAvailable`・`recycleCapacity`・`hasWin32UnsafeComponent`） | TestNewPlanTrashPrecheck、TestTrashPrecheckWindows、TestTrashPrecheckCapacity | 共通・Windows・NUKE/SMALL |
| 実行時にもう一度、今の大きさで確かめる（`trash.go` `trashItem`） | TestTrashExecuteRecheck（本物のごみ箱は呼ばない。`noRealTrash`） | Windows・SMALL |
| 計画時に使えない項目を、実行時に何もせず失敗にする（`execute.go` `run`、`trash.go` `trashItem`） | TestTrashWindowsUnavailable（本物のごみ箱は呼ばない。`noRealTrash`）、TestTrashUnavailable | Windows・ubuntu・macOS（`CGO_ENABLED=0`） |
| ごみ箱へ移す呼び出しの結果を、呼び出しが返した成否ではなく、呼び出しの後の状態で決める（`trash.go` `trashOutcome`。§12.1）。成功を返しても残っていれば Failed、失敗を返してもごみ箱に入っていれば Done、消えたのにごみ箱の中の項目がなければ TrashUnconfirmed（完全に削除された可能性を伝える） | TestTrashReportedButLeft、TestTrashOutcomeFromState（`trashCall` フックで呼び出しを差し替える） | Windows・macOS（cgo） |
| PreDeleteItem での中止（二つ目の防御。`trash_ifo_windows.go` `progressSink.preDelete`） | TestTrashPreDeleteAbort、TestProgressSink | TRASH・NUKE・Windows |
| 事前確認が見落とした最大サイズ超過の最後の防御（`FOF_WANTNUKEWARNING` の確認ダイアログ） | TestTrashOverCapacityBypass | TRASH・SMALL |
| フォルダの中身ごとの結果の扱い（`progressSink.postDelete`。最初のパスと最初の失敗）。成功の通知でも `psiNewlyCreated` が NULL なら完全削除として失敗にする（V18、§12.2 の手順 5） | TestProgressSink | Windows |
| COM の呼び出しに渡すもの（進捗通知の受け取り口など）をヒープに置き、解放まで生かす（`trash_ifo_windows.go` `comObj.call` の `//go:uintptrescapes`、`trashLocked`） | TestComCallPointerArgs | Windows |
| HRESULT の成否の判定（`trash_ifo_windows.go` `hresultFailed`） | TestHresultFailed | Windows |
| ごみ箱が使えないビルド（`trash_darwin_nocgo.go`、`trash_other.go`） | TestTrashUnavailable、TestNewPlanTrashPrecheck（事前確認）、TestTrashSysUnavailable（`trashSys` 自体が何も消さない） | ubuntu・macOS（`CGO_ENABLED=0`） |

## I6 ファイル名を変換しない

| 経路 | 防いでいるテスト | 条件 |
|---|---|---|
| コピー先の名前はコピー元の名前をそのまま使う（`plan.go` `item`、`copy.go`） | TestCopyTree（日本語・絵文字・NFD） | 共通 |
| 移動先の名前は移動元の名前をそのまま使う（同一ボリュームの `move.go` `rename`・`mergeEntry`、ボリュームをまたぐ `copyThenRemove` のコピーと記録した名前での削除。日本語・絵文字・NFD の名前を、トップレベルの項目・フォルダの中身・マージ先の中身に置く） | TestMoveNamesSameVolume、TestMoveNamesCrossVolume | 共通・CROSSVOL |
| macOS の exFAT で、列挙が NFD の名前を返す NFC の名前のファイル（名前を変換して探し直さず、失敗として報告する。§8.5、V17） | TestNFCOnExFATCopy、TestNFCOnExFATDelete、TestNFCOnExFATMove | macOS・EXFAT |
| Windows の `\\?\` 変換（`path_windows.go` `sysPath`・`userPath`） | TestSysPathWindows、TestUserPathWindows、TestRenameHelpersWin32UnsafeNames、TestReadDirWin32UnsafeNames、TestFileIDWin32UnsafeNames、TestLstatEntryWin32UnsafeNames | Windows |
| フォルダの作成の名前（`mkdir.go` `Mkdir`。名前をそのまま使い、Rename と同じ検査で使えない名前を拒む。末尾が `.` の親の中に、同名の別フォルダと取り違えずに作る。フェーズ17） | TestMkdir（日本語・NFD）、TestMkdirInvalidName、TestMkdirPaths（長いパス・`foo.` の親・リンクの親） | 共通 |
| 一覧のための調べ・列挙の名前とパス（`list.go`。列挙で得た名前をそのまま返し、`\\?\` を通す。フェーズ17） | TestListLongPathAndUnsafeNames、TestLstatErrors・TestReadDirEntersLinkedFolder（エラーのパスに `\\?\` が付かない） | 共通 |
| プレビューのための先頭の読み取り（`readhead*.go` `ReadHead`。`\\?\` を通し、末尾が `.` の名前で同名の別ファイルを読まない。読むだけで変更しない。フェーズ18） | TestReadHeadPaths（長いパス・`foo.` と `foo`）、TestReadHead（読んだ後に木が変わらない）、TestReadHeadErrors（エラーのパスに `\\?\` が付かない） | 共通 |
| 末尾が `.`・空白の名前、予約名を含むコピー・移動で、同名の別ファイル（`foo`）と取り違えない（`\\?\` 変換を通る `copy.go`・`move.go`・`remove.go` の各経路） | TestCopyWin32UnsafeNames、TestMoveWin32UnsafeNames、TestMoveCrossVolumeWin32UnsafeNames | 共通・CROSSVOL |
| 260 文字を超えるパスのコピー・移動（上書き・自動リネーム・マージ・移動元の削除を含む） | TestCopyLongPath、TestMoveLongPath、TestMoveCrossVolumeLongPath | 共通・CROSSVOL |
| 自動リネームの候補（`copy.go` `autoRenameName`。切り詰めない） | TestAutoRenameName、TestCopyAutoRenameTooLong | 共通 |
| `Rename` の大文字小文字・正規化だけの変更（`rename.go` `Rename`。Windows の exFAT・FAT32 では 2 段階の変更） | TestRenameCaseOnly、TestRenameCaseOnlyOtherVolumes | 共通・EXFAT/FAT32 |
| macOS の exFAT・FAT32 の AppleDouble の付属の除外（`appledouble_darwin.go`。名前をバイト単位で照合し、変換しない） | TestAppleDoubleNamesOrdinary、TestAppleDoubleQuarantineOtherVolumes | 共通・macOS・EXFAT/FAT32 |
| ごみ箱に名前を変換せずに渡す（`trash_darwin_cgo.go` のファイルシステムの表現、`trash_windows.go` の正規化で変わる名前の拒否） | TestTrashWindowsUnavailable（`foo.` を入れようとしても `foo` が残る）。TestTrash の日本語の名前は、NFC・NFD や大文字小文字の違いを含まないので変換を判別しない | Windows |
| リンク先の文字列を書き換えない（`copy.go` `copySymlink`） | TestCopyTreeWithLinks、TestCopyWindowsSymlinkKind | 共通・Windows |

## I7 キャンセル後・失敗後も I1〜I6 が成り立つ

キャンセル・失敗の経路ごとに、上の性質が保たれることを確かめているテスト。

| 経路 | 防いでいるテスト | 条件 |
|---|---|---|
| 実行前のキャンセル（`execute.go` `run`） | TestExecuteCanceledBeforeStart | 共通 |
| コピー中のキャンセル・失敗（I1・I3） | TestCopyCancelMidFile、TestCopyCancelInFolder、TestCopyWriteFailure、TestCopyNoSpaceInjected、TestCopySourceChangedDuringCopy、TestFileSyncFailure | 共通 |
| 完全削除中のキャンセル・失敗（I4） | TestDeleteCancel、TestDeleteLocked、TestDeleteReadOnly | 共通・Windows |
| 移動中のキャンセル・失敗（I2・I4） | TestMoveCrossVolumeFault、TestMoveCrossVolumeCancel、TestMoveCrossVolumeCancelRemoval、TestMoveMergeCancel、TestMoveSyncFailureKeepsSource | 共通・CROSSVOL |
| リンクの作成の失敗（I1） | TestCopySymlinkCreateFails、TestCopySymlinkToFATVolumes | 共通・EXFAT/FAT32 |
| 使用中のやり直しの待ちの間のキャンセル（`lockretry.go` `retry`・`wait`、各呼び出し側の KindCanceled の扱い） | TestLockRetrier（cancel during wait）、TestLockRetryCopyCancel、TestLockRetryDeleteCancel、TestLockRetryCancelPaths（コピー元を開く・検証・上書き・自動リネーム・移動・マージの中身・マージの後の移動元の削除）、TestLockRetryCancelSourceRemoval | 共通・CROSSVOL |

---

## テストで守られていない経路と、受け入れた危険

見つかったものを挙げる。「（解決済み）」は直してテストしたもの、それ以外は残っているもの。

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
13. （確かめられない）§17.1 の使用中の一時的な失敗のやり直しの間隔と上限（1 操作 1 秒、1 回の Execute で 10 秒）が、実際のウイルス対策ソフト・
    検索インデクサ・同期クライアントのロックに足りるか。CI では再現できないので、推測で決めた値のまま。やり直しの処理自体は、注入した失敗で 3 つの OS でテストし
    （TestLockRetry*）、実際の共有違反が上限の後に KindLocked になることは TestCopyLockedSource などでテストしている（Windows）。
14. （解決済み。2026-09-25 の監査）fileID を記録した後の一時ファイルの削除が、名前だけで消していた（§10.1 の手順 8 と違い、一時ファイルを置き換えたものを消しうる）。
    照合してから消すように直した（TestTempReplacedBeforeVerify）。
15. （解決済み。2026-09-25 の監査）Windows のごみ箱で、最大サイズを超えて完全に削除された項目（`psiNewlyCreated` が NULL）を Done と報告しうる（I5）。
    `OutcomeTrashUnconfirmed`（`KindTrashUnavailable`）にするように直した（TestProgressSink、TestTrashOutcomeFromState。§12.1、§12.2 の手順 5）。
    あわせて、ごみ箱に入ったのに失敗と報告する逆向きの誤りも、呼び出しの後の状態で結果を決める規則（§12.1）で直した。項目はすでに削除されているので、報告を正すだけで、防御は事前確認と `FOF_WANTNUKEWARNING`。
16. （解決済み。2026-09-25 の監査）Windows の Zone.Identifier をパスで書くとき、照合の後に名前をリンクへ置き換えられると、リンク先に書きえた（I4）。
    照合に使うハンドルを、名前の変更を許さずに開くように直した（TestCopyZoneIdentifierLinkSwap。属性だけのアクセス権のハンドルは共有モードの検査の対象にならないので、読み取りのアクセス権も要求する）。
17. （解決済み。2026-09-25 の監査）ごみ箱が使えないことを確かめるテストの一部が `FSOPS_TEST_TRASH` を見ず、防御が壊れると本物のごみ箱を呼びえた。
    ごみ箱へ移す呼び出しを、呼ばれたら失敗するものに差し替えた（`noRealTrash`）。
18. （解決済み。2026-09-25 の監査）`Rename` の 2 段階の変更（Windows の exFAT・FAT32 の大文字小文字だけの変更）の途中でプロセスが強制終了するか、元に戻せなかった場合、
    利用者のファイル・フォルダが一時ファイルと同じ形の名前（`.fsops-<16 進>.tmp`）で残り、ゴミとして消されるおそれがあった。
    途中名を `.fsops-rename-<16 進>` に変え（SPEC §11.3）、`doc.go` に扱いを書いた。2 回目の変更の失敗と元に戻す段を、
    `caseRenameNoop` フックで 2 段階の変更を通して TestRenameTwoStep・TestRenameTwoStepSecondFails・TestRenameTwoStepRollbackFails でテストした（大文字小文字を区別しないフォルダ。Linux の ext4 では Skip）。
19. （受け入れる。仕様の限界）§13.3 の照合は、大きさと更新日時が同じ編集を見分けられない。更新日時の単位が粗い FAT32（2 秒）などで、
    コピーの後の同じ単位の中で大きさを変えずに書き換えられると、その編集は移動元の削除で失われうる。
20. （受け入れる。起きにくい）EINTR のやり直し（SPEC §4）で、作成系の呼び出し（`mkdirat`・`symlinkat`・`O_CREAT|O_EXCL` の open・排他リネーム）が
    完了した後に EINTR を返した場合、やり直しが EEXIST になり、成功を失敗・計画後に現れた衝突として報告しうる。§8.4 の代わりの手段では、確保した空のファイルが残りうる。
    上書き・データの損失は起きない（`os` パッケージのやり直しも同じ性質）。
21. （テストされていない）次の経路は、防御はあるがテストで判別されない。
    - 最終名にした後の照合（`copy.go` `finalize`）と、finalize の冒頭のキャンセルの確認。
    - §8.4 の代わりの手段で、確保の後に失敗したときの片付け（`rename_unix.go` の手順 2・4、`removeIfSame`）。
    - `openSecDir` の二重の防御（O_NOFOLLOW と fileID の照合）の片方ずつ、Unix の `setMetaIn` の O_NOFOLLOW だけ。
    - Unix の移動元の削除のやり直しは、§13.3 の照合（`matches`）をやり直さない。Unix では使用中のやり直しが起きない（`lockRetrySys` が偽）ので、注入した失敗でだけ通る。
    - ごみ箱: パスの途中の予約名（`root\CON\x.txt`）の事前確認、ごみ箱へ渡す名前の NFC・NFD・大文字小文字（TRASH）。`PostDeleteItem` が届かない場合（`!posted`）の報告は、
      §12.1 の規則でどの状態でも正しくなるが（TestTrashOutcomeFromState）、Windows のその分岐自体はテストされていない。
    - 名前: 不正な UTF-16（Windows）・不正な UTF-8（Linux）の名前、macOS での大文字小文字だけが違うトップレベルのコピー。
    - 移動での容量不足、書き込み中に上限を超える KindFileTooLarge、検証でのコピー元の変化（コピーではテストしている。実験では移動元が残った）。
22. （要確認）Windows の exFAT・FAT32 で、同じ名前のコピー元 2 件の両方に上書きを承認すると、ファイルインデックスがディレクトリエントリの位置に基づく（V16）ため、
    2 件目の照合（`checkTarget`）が 1 件目の書いたファイルと一致して、それを上書きしうる（大きさと更新日時も一致する場合）。
23. （解決済み。2026-09-25 の報告の見直し）Unix の完全削除が、フォルダの中のマウントポイントに入り、マウントされた別のボリュームの中身を消していた
    （最後の `rmdir` が `EBUSY` になり「使用中」とだけ報告された）。マウントポイントには入らず `KindMountPoint` で報告するように直した（§13.1）。
    Linux の同じデバイスのバインドマウントは見分けられない（受け入れる）。Linux ではテストの中でマウントできないので、macOS だけでテストしている。
24. （解決済み。2026-09-25 の報告の見直し）報告と実際の状態の食い違いを直した。
    - macOS の exFAT・FAT32 の空のファイルは fileID が操作のたびに変わり（V25）、そこへのコピーが毎回失敗して一時ファイルが残っていた。
      空のファイルの仮の ino を一定の値にした（§8.3。TestEmptyFilesOtherVolumes、TestIDStatEmptySynthetic）。
    - 移動元のハードリンクへの上書き移動が、何もせずに Done になっていた（Unix の rename の仕様）。`KindSameFile` の失敗にした（§7.3。TestMoveOverwriteHardLinkToSource）。
    - 完全削除で、確かめた後にトップレベルの項目が移されると、何も消していないのに Done になっていた。`KindNotFound` の失敗にした（§7.3。TestDeleteTopLevelMovedAway）。
    - 最終名にする直前に一時ファイルが置き換えられた場合、最終名に置き換えたものが置かれたのに Failed と報告していた。Partial にした（§10.1。TestTempReplacedDuringFinalRename）。
    - ボリュームをまたぐ移動で、コピーの後に移動元へ追加されたファイル（移動先にない）を報告していなかった。1 件ずつ報告するようにした（§13.3。TestRemoveRecordedReportsAdded）。
25. （解決済み。2026-09-25 の報告の設計の見直し）SPEC の規定どおりでも利用者を誤解させる報告を直した（P1〜P7）。
    コピー先側のエラーに `OnDest` を付け、コピー先側の置き換えを `KindDestChanged` にした（P1）。`ItemResult.Method` に実際に使った方式を入れ、
    方式ごとの Partial の意味を §7.4 に書いた（P2）。保持しないメタデータ（タグ・`user.*`・代替データストリーム）を警告する（P3）。
    `LinkSkip` のスキップをエラーにしない（P4）。ごみ箱の事前確認で確かめられなかった場合を `KindTrashUnavailable` にしない（P5）。
    macOS の exFAT の NFC の名前を `KindNameForm` にした（P6）。検証・同期の失敗を `KindVerifyFailed`・`KindSyncFailed` にした（P7）。
26. （解決済み。2026-09-25、V26）Windows の削除待ち（ほかのプロセスが開いたまま削除の印を付けたもの）を「権限がありません」、
    削除待ちの名前だけが残るフォルダを「空ではありません」と報告し、やり直しもしていなかった。失敗の直後の NT ステータス
    （`STATUS_DELETE_PENDING`）で見分けて `KindLocked` にし、§17.1 のとおりやり直すようにした（TestDeletePendingByOther、
    TestDeleteFolderWithPendingChildren。Windows・EXFAT/FAT32）。
