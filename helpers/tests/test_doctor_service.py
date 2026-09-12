"""Public/private service command and default path contract."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock
sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'tmux'))
import cockpit_doctor as doctor

class DoctorServiceTests(unittest.TestCase):
    def test_doctor_accepts_only_actual_service_argv(self):
        good = [
            '/opt/homebrew/Frameworks/Python.framework/Versions/3.14/Resources/Python.app/Contents/MacOS/Python /x/cockpit_public.py --install-config /x/pin --helper tmux.services hygiene --config /x/config',
            'python3 /x/services.py hygiene --config /x/lifecycle.json',
            'python3 -m helpers.tmux.services hygiene',
            'python3 /x/cockpit_public.py --install-config /x/pin --helper tmux.services hygiene --config /x/config',
            'env CASS_TMUX_HYGIENE_INTERVAL=60 python3 /x/cockpit_public.py --helper=tmux.services --install-config=/x/pin hygiene',
        ]
        bad = [
            'python3 other.py --helper tmux.services hygiene',
            'python3 cockpit_public.py --install-config --helper tmux.services hygiene',
            'python3 cockpit_public.py --install-config "--helper tmux.services hygiene"',
            'python3 cockpit_public.py --unknown value --helper tmux.services hygiene',
            'echo python3 services.py hygiene',
        ]
        for command in good + bad:
            with self.subTest(command=command):
                def runner(args):
                    return subprocess.CompletedProcess(args, 0, command if args[0] == 'ps' else '123', '')
                self.assertEqual(doctor.check_janitor_command('fixture', runner).status, 'ok' if command in good else 'warn')

    def test_paths_follow_lifecycle_defaults_and_overrides(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            with mock.patch.dict(os.environ, {'HOME': tmp}, clear=True):
                args = doctor.build_parser().parse_args([])
                self.assertEqual(args.hygiene_log, str(root / '.local/state/openclaw-cockpit/logs/janitor.log'))
                self.assertEqual(args.inspector_log, str(root / '.local/state/openclaw-cockpit/logs/inspector.log'))
                config = root / 'lifecycle.json'
                config.write_text(json.dumps({'log_dir':str(root / 'custom'), 'inspector_log_dir':str(root / 'inspect')}))
                os.environ['OPENCLAW_COCKPIT_LIFECYCLE_CONFIG'] = str(config)
                args = doctor.build_parser().parse_args([])
                self.assertEqual(args.hygiene_log, str(root / 'custom/janitor.log'))
                self.assertEqual(args.inspector_log, str(root / 'inspect/inspector.log'))
