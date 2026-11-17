"""Private trusted SDK scorer. All SQL and code are application-owned.

Scoring submitted labels establishes neither holdout custody nor absence of leakage.
Resource limits deliberately bound CSV records, report cardinality, and SQLite pages.
"""
import csv
import errno
from decimal import Decimal, ROUND_HALF_EVEN
import hashlib
import json
import math
import os
import re
import resource
import sqlite3
import stat
import sys

MAX_BYTES = 2_000_000_000
MAX_SCRATCH_FILE_BYTES = 8_000_000_000
CHUNK = 256 * 1024
FAMILIES = {
    'AUROC': 'AUROC', 'ROCAUC': 'AUROC', 'AUCROC': 'AUROC', 'AUC': 'AUROC',
    'PRAUC': 'PRAUC', 'AUCPR': 'PRAUC', 'AUPRC': 'PRAUC', 'AVERAGEPRECISION': 'PRAUC',
    'ACCURACY': 'ACCURACY', 'ACC': 'ACCURACY', 'F1': 'F1', 'F1SCORE': 'F1',
}
NUMBER = r'[-+]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][-+]?\d+)?'


class PredictionInputError(ValueError):
    pass


def fail(message):
    raise PredictionInputError(message)


def declaration(value):
    if isinstance(value, str):
        text = value.strip()
        if text.startswith(('[', '{')):
            try:
                parsed = json.loads(text)
            except json.JSONDecodeError:
                fail('Scoring declaration is not valid JSON')
            return declaration(parsed)
        row = next((re.sub(r'^\s*\d+\.\s*', '', line.strip()) for line in text.splitlines() if line.strip()), '')
        cells = {}
        for part in row.split(' | '):
            if ':' in part:
                k, v = part.split(':', 1)
                cells[k.strip().lower()] = v.strip()
        if 'metric' not in cells:
            cells['metric'] = re.split(r'[<>=]', row, maxsplit=1)[0].strip()
        value = cells
    if isinstance(value, list):
        if not value:
            fail('Declare a primary metric in row one')
        value = value[0]
    if not isinstance(value, dict):
        fail('Declare a primary metric and its operating point')
    cells = {str(k).lower(): v for k, v in value.items()}
    name = cells.get('metric')
    if not isinstance(name, str) or not name.strip():
        fail('Declare a primary metric in row one')
    family = FAMILIES.get(re.sub('[^A-Z0-9]', '', name.upper()))
    if family is None:
        fail('Unsupported metric %r: use AUROC, PR-AUC (average precision), accuracy or F1' % name)
    operating = cells.get('operatingpoint') or None
    if operating is not None and not isinstance(operating, str):
        fail('Operating point must be text')
    point = parse_point(operating) if operating and family in ('ACCURACY', 'F1') else None
    if family in ('ACCURACY', 'F1') and point is None:
        fail('Accuracy and F1 require a declared operating point')
    return name.strip(), family, operating, point


def parse_point(text):
    t = text.strip()
    match = re.fullmatch(r'top\s+(' + NUMBER + r')\s*%', t, re.I)
    if match:
        rate = Decimal(match[1])
        if not 0 <= rate <= 100:
            fail('Top percentage must be between 0 and 100')
        return ('rate', rate / 100)
    match = re.fullmatch(r'top\s+(\d+)', t, re.I)
    if match:
        return ('count', int(match[1]))
    match = re.fullmatch(r'(?:(?:threshold|score|cutoff)\s*:?\s*)?(>=|>)?\s*(' + NUMBER + ')', t, re.I)
    if match:
        number = float(match[2])
        if not math.isfinite(number):
            fail('Operating point must be finite')
        return ('strict' if match[1] == '>' else 'threshold', number)
    fail('Unsupported operating point; use threshold 0.8, > 0.8, top 20 or top 2%')


def identity(info):
    return tuple(getattr(info, key) for key in ('st_dev', 'st_ino', 'st_size', 'st_mtime_ns', 'st_ctime_ns', 'st_nlink'))


def open_regular(path):
    # Walk every component with NOFOLLOW, including parent folders.
    if not os.path.isabs(path) or '..' in path.split(os.sep):
        fail('Predictions must use an absolute path without parent traversal')
    parts = [part for part in path.split(os.sep) if part and part != '.']
    if not parts:
        fail('Predictions must name a CSV file')
    parent = os.open('/', os.O_RDONLY | os.O_DIRECTORY)
    try:
        for part in parts[:-1]:
            next_dir = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=parent)
            os.close(parent)
            parent = next_dir
        fd = os.open(parts[-1], os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=parent)
    except OSError as exc:
        if exc.errno in (errno.ENOENT, errno.ENOTDIR, errno.ELOOP, errno.EACCES):
            fail('Cannot read predictions.csv as a regular file: %s' % exc)
        raise
    finally:
        os.close(parent)
    stream = os.fdopen(fd, 'rb')
    info = os.fstat(fd)
    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or not 0 < info.st_size <= MAX_BYTES:
        stream.close()
        fail('Predictions must be a nonempty regular, unlinked file up to 2 GB')
    return stream, info


def digest(stream, expected, destination=None):
    stream.seek(0)
    total = 0
    hash_value = hashlib.sha256()
    while True:
        chunk = stream.read(CHUNK)
        if not chunk:
            break
        total += len(chunk)
        if total > expected:
            fail('Predictions changed during inspection')
        hash_value.update(chunk)
        if destination:
            destination.write(chunk)
    if total != expected:
        fail('Predictions changed during inspection')
    return hash_value.hexdigest()


def lines(stream):
    while True:
        raw = stream.readline(65537)
        if not raw:
            break
        if len(raw) > 65536:
            fail('CSV physical lines must not exceed 64 KiB')
        yield raw.decode('utf-8', errors='strict')


def score(request, folder):
    # This is a per-file ceiling, not a total scratch-folder quota. SQLite sort
    # runs and rollback journals may coexist with the database and snapshot.
    soft, hard = resource.getrlimit(resource.RLIMIT_FSIZE)
    ceiling = min(MAX_SCRATCH_FILE_BYTES, hard) if hard != resource.RLIM_INFINITY else MAX_SCRATCH_FILE_BYTES
    if soft != resource.RLIM_INFINITY:
        ceiling = min(ceiling, soft)
    resource.setrlimit(resource.RLIMIT_FSIZE, (ceiling, ceiling))
    metric, family, operating, point = declaration(request.get('declaration'))
    source, before = open_regular(request['predictions'])
    try:
        snapshot = os.path.join(folder, 'predictions.csv')
        with open(snapshot, 'xb') as copied:
            sha = digest(source, before.st_size, copied)
        if sha != digest(source, before.st_size) or identity(before) != identity(os.fstat(source.fileno())):
            fail('Predictions changed during inspection')
        with sqlite3.connect(os.path.join(folder, 'scores.sqlite')) as db:
            db.execute('PRAGMA temp_store=FILE')
            db.execute('PRAGMA cache_size=-8192')
            db.execute('PRAGMA mmap_size=0')
            db.execute('PRAGMA max_page_count=1953125')
            db.execute('CREATE TABLE rows (id INTEGER PRIMARY KEY, label INTEGER NOT NULL, score REAL NOT NULL, slice TEXT, decision INTEGER NOT NULL DEFAULT 0)')
            csv.field_size_limit(65536)
            seen = set()
            n = positives = 0
            with open(snapshot, 'rb') as copied:
                reader = csv.reader(lines(copied), strict=True)
                header = next(reader, None)
                if header not in (['y_true', 'y_score'], ['y_true', 'y_score', 'slice']):
                    fail('CSV header must be y_true,y_score[,slice]')
                for row in reader:
                    if len(row) != len(header):
                        fail('CSV row has the wrong number of columns')
                    try:
                        label, value = float(row[0]), float(row[1])
                    except ValueError:
                        fail('CSV labels and scores must be numbers')
                    if not math.isfinite(label) or label not in (0, 1):
                        fail('Binary labels must be finite 0 or 1, with 1 positive')
                    if not math.isfinite(value):
                        fail('Prediction scores must be finite')
                    key = row[2] if len(header) == 3 else None
                    if key is not None:
                        if len(key.encode('utf-8')) > 512:
                            fail('Slice names must not exceed 512 UTF-8 bytes')
                        seen.add(key)
                        if len(seen) > 512:
                            fail('Predictions must not exceed 512 slices')
                    n += 1
                    positives += int(label)
                    db.execute('INSERT INTO rows (id,label,score,slice) VALUES (?,?,?,?)', (n, int(label), value, key))
                    if n % 10000 == 0:
                        db.commit()
            if not n:
                fail('Predictions must contain at least one data row')
            db.commit()
            db.execute('CREATE INDEX score_order ON rows(score DESC,id)')
            db.execute('CREATE INDEX slice_order ON rows(slice,score DESC,id)')
            policy = None
            if family in ('ACCURACY', 'F1'):
                kind, number = point
                if kind in ('threshold', 'strict'):
                    db.execute('UPDATE rows SET decision = (score ' + ('>' if kind == 'strict' else '>=') + ' ?)', (number,))
                    policy = {'kind': 'threshold', 'comparison': '>' if kind == 'strict' else '>=', 'threshold': number}
                else:
                    k = int((number * n).quantize(Decimal(1), rounding=ROUND_HALF_EVEN)) if kind == 'rate' else number
                    if k > n:
                        fail('Top count exceeds prediction rows; the operating point cannot be silently clamped')
                    db.execute('UPDATE rows SET decision=1 WHERE id IN (SELECT id FROM rows ORDER BY score DESC,id LIMIT ?)', (k,))
                    policy = {'kind': 'top', 'selected': k, 'tieBreak': 'CSV row order', 'rounding': 'half-even' if kind == 'rate' else None}
            def compute(key=None, is_slice=False):
                where, args = ('WHERE slice=?', (key,)) if is_slice else ('', ())
                count, pos = db.execute('SELECT count(*),sum(label) FROM rows ' + where, args).fetchone()
                reason = None
                if family == 'AUROC' and pos in (0, count):
                    reason = 'AUROC requires both binary classes'
                if family == 'PRAUC' and pos == 0:
                    reason = 'Average precision requires positive labels'
                if reason:
                    return count, pos, None, reason
                if family in ('AUROC', 'PRAUC'):
                    groups = db.execute('SELECT score,count(*),sum(label) FROM rows ' + where + ' GROUP BY score ORDER BY score DESC', args)
                    tp = fp = 0
                    value = 0.0
                    for _, group_n, group_pos in groups:
                        if family == 'AUROC':
                            value += group_pos * ((count - pos) - fp - (group_n - group_pos) / 2)
                        tp += group_pos
                        fp += group_n - group_pos
                        if family == 'PRAUC':
                            value += (group_pos / pos) * (tp / (tp + fp))
                    return count, pos, value / (pos * (count - pos)) if family == 'AUROC' else value, None
                tp, fp, fn, correct = db.execute('SELECT sum(label=1 AND decision=1),sum(label=0 AND decision=1),sum(label=1 AND decision=0),sum(label=decision) FROM rows ' + where, args).fetchone()
                if family == 'F1' and 2 * tp + fp + fn == 0:
                    return count, pos, None, 'F1 is undefined with no positive labels or positive decisions'
                return count, pos, correct / count if family == 'ACCURACY' else 2 * tp / (2 * tp + fp + fn), None
            _, _, value, reason = compute()
            if reason:
                fail('%s (%d of %d rows positive)' % (reason, positives, n))
            slices = []
            for key, in db.execute('SELECT slice FROM rows WHERE slice IS NOT NULL GROUP BY slice ORDER BY min(id)'):
                count, pos, slice_value, slice_reason = compute(key, True)
                item = {'slice': key, 'n': count, 'positives': pos, 'value': slice_value}
                if slice_reason:
                    item['reason'] = slice_reason
                slices.append(item)
        current, after = open_regular(request['predictions'])
        try:
            if identity(before) != identity(os.fstat(source.fileno())) or identity(before) != identity(after) or digest(current, after.st_size) != sha:
                fail('Predictions changed during scoring')
        finally:
            current.close()
        return {'metric': metric, 'family': family, 'value': value, 'n': n, 'positives': positives,
                'operatingPoint': operating, 'slices': slices, 'predictionsSha256': sha, 'scorer': 'platform',
                'decisionPolicy': policy, 'scope': 'Scores submitted binary labels; does not establish holdout custody or absence of leakage.'}
    finally:
        source.close()


if __name__ == '__main__':
    try:
        request = json.loads(sys.stdin.buffer.read(65537))
        print(json.dumps(score(request, sys.argv[1]), allow_nan=False))
    except (PredictionInputError, UnicodeError, csv.Error) as exc:
        print('PREDICTION_INPUT_INVALID: ' + str(exc), file=sys.stderr)
        sys.exit(2)
    except Exception as exc:
        print('Scorer infrastructure failure: %s: %s' % (type(exc).__name__, exc), file=sys.stderr)
        sys.exit(1)
