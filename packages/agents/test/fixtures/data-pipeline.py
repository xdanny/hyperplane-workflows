"""Deterministic test author: executes real CSV controls and SQLite builds.

This is test transport evidence, not model-generated implementation evidence.
"""
import csv
import hashlib
import json
import platform
import sqlite3
import sys
import time
from decimal import Decimal
from pathlib import Path

phase, config_file = sys.argv[1:]
config = json.loads(Path(config_file).read_text())
inputs = config['inputs']
root = Path.cwd()
direct = config.get('layout') == 'direct'
source = Path(inputs['sources'])
started = time.monotonic()
rows = list(csv.DictReader(source.open()))
identity = hashlib.sha256(source.read_bytes()).hexdigest()
kept = [r for r in rows if r['is_test'] == '0']
control = sum((Decimal(r['amount']) for r in kept), Decimal(0))
measure = json.loads(inputs['measures'])[0]
measure.setdefault('unit', 'exact')
measure.setdefault('tolerance', '0')
control_query = 'sum(Decimal(row["amount"]) for row in csv.DictReader(source) if row["is_test"] == "0")'
control_path = 'independent csv.DictReader plus Decimal; no SQLite import'
grain_query = 'SELECT COUNT(*)-COUNT(DISTINCT order_day), SUM(order_day IS NULL) FROM daily_sales'

def write(name, value):
    (root / name).write_text(json.dumps(value, indent=2) + '\n')

if phase == 'profile':
    write('source-profile.json', {
        'declaredSources': inputs['sources'],
        'sources': [{'name': 'orders', 'readPath': str(source), 'identity': identity, 'verified': True,
            'rows': len(rows), 'scanned': 'full',
            'columns': [{'name': name, 'type': 'text', 'nullCount': sum(not r[name] for r in rows),
                'distinctCount': len({r[name] for r in rows if r[name]})} for name in rows[0]],
            'candidateKeys': [{'columns': ['id'], 'unique': True, 'duplicateRows': 0}],
            'observedRange': '2026-09-01 through 2026-09-02',
            'observedLag': 'Static snapshot; source max date equals delivered max date', 'issues': []}],
        'controls': [{**measure, 'value': str(control), 'query': control_query, 'derivationPath': control_path}],
        'assumptions': [], 'openQuestions': []})
elif phase == 'rules':
    staging = {'name': 'stg_orders', 'grain': 'one row per source id', 'keyColumns': ['id'],
        'upstreams': [{'kind': 'source', 'name': 'orders'}],
        'columns': [{'name': name, 'type': typ, 'description': desc} for name, typ, desc in [
            ('id', 'TEXT', 'Source order identifier'), ('order_day', 'TEXT', 'UTC order date'),
            ('amount_cents', 'INTEGER', 'Order amount in cents')]],
        'materialization': 'table', 'incrementalStrategy': 'full replacement', 'tests': ['id is unique']}
    mart = {'name': 'daily_sales', 'grain': inputs['grain'], 'keyColumns': ['order_day'],
        'upstreams': [{'kind': 'model', 'name': 'stg_orders'}],
        'columns': [{'name': 'order_day', 'type': 'TEXT', 'description': 'UTC date'},
            {'name': 'net', 'type': 'INTEGER', 'description': 'Daily amount in cents'}],
        'materialization': 'table', 'incrementalStrategy': 'full replacement', 'tests': ['order_day is unique and nonnull']}
    if direct:
        mart['upstreams'] = [{'kind': 'source', 'name': 'orders'}]
    write('pipeline-plan.json', {'targetStore': inputs['targetStore'], 'outputModel': 'daily_sales', 'models': [mart] if direct else [staging, mart],
        'measureMap': [{**measure, 'model': 'daily_sales', 'column': 'net', 'expression': 'SUM(CAST(round(CAST(amount AS NUMERIC)*100) AS INTEGER))' if direct else 'SUM(amount_cents)',
            'exclusions': ['is_test=1'], 'controlQuery': control_query, 'controlPath': control_path}],
        'freshness': {'maxLag': inputs['maxLag'], 'refreshDeadline': inputs.get('refreshDeadline', ''),
            'lateArrival': inputs.get('lateArrival', ''), 'verification': 'Compare source and mart maximum dates',
            'lateArrivalStrategy': 'No late-arrival policy declared'},
        'safeUseNotes': 'No customer email in staging or mart', 'publicationPlan': 'Only daily_sales in delivery SQLite',
        'rejectedDesigns': []})
elif phase == 'audit':
    plan = json.loads(Path(config['rules'], 'pipeline-plan.json').read_text())
    for folder in ['models', 'delivery', 'fixtures']:
        (root / folder).mkdir(exist_ok=True)
    staging_sql = '''CREATE TABLE stg_orders AS SELECT id, substr(ordered_at,1,10) order_day,
      CAST(round(CAST(amount AS NUMERIC)*100) AS INTEGER) amount_cents FROM raw_orders WHERE is_test='0';'''
    mart_sql = 'CREATE TABLE daily_sales AS SELECT order_day, SUM(amount_cents) net FROM stg_orders GROUP BY order_day;'
    if direct:
        staging_sql = ''
        mart_sql = '''CREATE TABLE daily_sales AS SELECT substr(ordered_at,1,10) order_day,
          SUM(CAST(round(CAST(amount AS NUMERIC)*100) AS INTEGER)) net FROM raw_orders WHERE is_test='0' GROUP BY order_day;'''
    else:
        (root / 'models/stg_orders.sql').write_text(staging_sql)
    (root / 'models/daily_sales.sql').write_text(mart_sql)
    (root / 'checks.sql').write_text(grain_query + ';\n')
    write('manifest.json', {'targetStore': inputs['targetStore'], 'outputModel': plan['outputModel'],
        'models': [{**m, 'file': 'models/' + m['name'] + '.sql'} for m in plan['models']],
        'toolchain': {'runtime': 'Python ' + platform.python_version() + '; SQLite ' + sqlite3.sqlite_version, 'packages': []}})
    def build():
        db = sqlite3.connect(':memory:')
        db.execute('CREATE TABLE raw_orders(id TEXT, ordered_at TEXT, amount TEXT, is_test TEXT, customer_email TEXT)')
        db.executemany('INSERT INTO raw_orders VALUES (?,?,?,?,?)', [tuple(r.values()) for r in rows])
        db.executescript(staging_sql + mart_sql)
        result = db.execute('SELECT order_day,net FROM daily_sales ORDER BY order_day').fetchall()
        retained = db.execute("SELECT COUNT(*) FROM raw_orders WHERE is_test='0'" if direct else 'SELECT COUNT(*) FROM stg_orders').fetchone()[0]
        duplicate, null = db.execute(grain_query).fetchone()
        assert duplicate == 0 and null == 0
        db.close()
        return result, retained
    before, retained = build()
    after, _ = build()
    assert before == after
    observed = Decimal(sum(row[1] for row in after)) / 100
    assert observed == control
    assert retained + len(rows) - len(kept) == len(rows)
    delivery = sqlite3.connect(root / 'delivery/pipeline.sqlite')
    delivery.execute('DROP TABLE IF EXISTS daily_sales')
    delivery.execute('CREATE TABLE daily_sales(order_day TEXT,net INTEGER)')
    delivery.executemany('INSERT INTO daily_sales VALUES (?,?)', after)
    tables = [row[0] for row in delivery.execute("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")]
    assert tables == ['daily_sales']
    source_date = max(row['ordered_at'][:10] for row in kept)
    assert delivery.execute('SELECT MAX(order_day) FROM daily_sales').fetchone()[0] == source_date
    delivery.commit()
    delivery.close()
    fixture = sqlite3.connect(':memory:')
    fixture.execute('CREATE TABLE daily_sales(order_day TEXT,net INTEGER)')
    fixture.executemany('INSERT INTO daily_sales VALUES (?,?)', after)
    clean = fixture.execute(grain_query).fetchone()
    assert clean == (0, 0)
    fixture.execute('INSERT INTO daily_sales VALUES (?,?)', after[0])
    changed = fixture.execute(grain_query).fetchone()
    assert changed == (1, 0)
    with (root / 'fixtures/duplicate.csv').open('w') as output:
        csv.writer(output).writerows([['order_day', 'net'], *after, after[0]])
    fixture.close()
    measured = [{'measure': measure['measure'], 'value': str(observed)}]
    write('verification-report.json', {
        'sources': [{'name': 'orders', 'identity': identity, 'rows': len(rows)}],
        'rowAccounting': [{'source': 'orders', 'inputRows': len(rows), 'retainedRows': retained,
            'contributedRows': retained, 'excluded': [{'filter': 'is_test=1', 'rows': len(rows)-len(kept)}],
            'query': "SELECT COUNT(*) FROM raw_orders WHERE is_test='0'; count CSV is_test=1" if direct else 'SELECT COUNT(*) FROM stg_orders; count CSV is_test=1'}],
        'grainCheck': {'model': 'daily_sales', 'grain': inputs['grain'], 'keyColumns': ['order_day'],
            'rows': len(after), 'duplicateRows': 0, 'nullKeyRows': 0, 'query': grain_query},
        'measures': [{**measure, 'expected': str(control), 'reproducedControl': str(control),
            'observed': str(observed), 'delta': str(observed-control), 'controlQuery': control_query,
            'controlPath': control_path, 'modelPath': 'SQLite raw_orders -> daily_sales' if direct else 'SQLite raw_orders -> stg_orders -> daily_sales', 'pass': True}],
        'negativeCase': {'source': 'model-fixture', 'file': 'fixtures/duplicate.csv', 'query': grain_query,
            'baseline': {'rows': len(after), 'duplicateRows': 0, 'nullKeyRows': 0},
            'mutated': {'rows': len(after)+1, 'duplicateRows': 1, 'nullKeyRows': 0}, 'detected': True},
        'publication': {'file': 'delivery/pipeline.sqlite', 'tables': tables, 'query': "SELECT name FROM sqlite_master WHERE type='table'"},
        'replay': {'rowsBefore': len(before), 'rowsAfter': len(after), 'measuresBefore': measured,
            'measuresAfter': measured, 'query': 'Full replacement build executed twice; compare ordered rows', 'pass': True},
        'freshness': {'maxLag': inputs['maxLag'], 'refreshDeadline': inputs.get('refreshDeadline', ''),
            'lateArrival': inputs.get('lateArrival', ''), 'observed': 'Source and mart maximum dates both ' + source_date,
            'query': 'SELECT MAX(order_day) FROM daily_sales; max CSV ordered_at date', 'pass': True,
            'lateCase': {'performed': False, 'observation': 'No late-arrival policy declared', 'query': '', 'pass': False}},
        'checks': [{'id': name, 'pass': True, 'expected': expected, 'observed': actual, 'query': query}
            for name, expected, actual, query in [
                ('row-accounting', str(len(rows)), str(retained + len(rows)-len(kept)), 'count retained and excluded'),
                ('key-uniqueness', '0 duplicate or null keys', '0 duplicate or null keys', grain_query),
                ('measure-reproduction', str(control), str(observed), control_query),
                ('negative-duplicate-key', '1 excess duplicate', '1 excess duplicate', grain_query)]],
        'passed': 4, 'failed': 0, 'timing': {'budget': 'No owner budget declared',
            'stages': [{'stage': 'build and checks', 'seconds': time.monotonic()-started}], 'withinBudget': True}})
else:
    raise ValueError('Unknown fixture phase')
