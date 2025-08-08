#!/usr/bin/env python3
"""
LaTeX Compiler Worker

Watches for job files in /work/latex_files/*/compile/queue/*.json
Processes LaTeX compilation and writes status/output files.
"""

import os
import json
import time
import subprocess
import shutil
import logging
from pathlib import Path
from datetime import datetime, timezone
from typing import Dict, Any, Optional
import signal
import sys

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(levelname)s - %(message)s'
)
logger = logging.getLogger(__name__)

class LatexCompilerWorker:
    def __init__(self, work_dir: str = "/work/latex_files"):
        self.work_dir = Path(work_dir)
        self.running = True
        self.poll_interval = 1.0  # seconds
        
        # Setup signal handlers for graceful shutdown
        signal.signal(signal.SIGTERM, self._signal_handler)
        signal.signal(signal.SIGINT, self._signal_handler)
        
        logger.info(f"LaTeX Compiler Worker starting, watching {self.work_dir}")

    def _signal_handler(self, signum, frame):
        logger.info(f"Received signal {signum}, shutting down gracefully...")
        self.running = False

    def run(self):
        """Main worker loop"""
        while self.running:
            try:
                self._process_pending_jobs()
                time.sleep(self.poll_interval)
            except Exception as e:
                logger.error(f"Error in main loop: {e}")
                time.sleep(self.poll_interval)
        
        logger.info("Worker stopped")

    def _process_pending_jobs(self):
        """Scan all projects for pending compilation jobs"""
        if not self.work_dir.exists():
            return
            
        for project_dir in self.work_dir.iterdir():
            if not project_dir.is_dir():
                continue
                
            queue_dir = project_dir / "compile" / "queue"
            if not queue_dir.exists():
                continue
                
            # Process all job files in queue
            for job_file in queue_dir.glob("*.json"):
                try:
                    self._process_job(job_file)
                except Exception as e:
                    logger.error(f"Error processing job {job_file}: {e}")

    def _process_job(self, job_file: Path):
        """Process a single compilation job"""
        try:
            with open(job_file, 'r') as f:
                job_data = json.load(f)
        except Exception as e:
            logger.error(f"Failed to read job file {job_file}: {e}")
            job_file.unlink(missing_ok=True)  # Remove invalid job
            return

        job_id = job_data.get("jobId")
        project_id = job_data.get("projectId")
        entry_file = job_data.get("entryFile", "main.tex")
        engine = job_data.get("engine", "pdflatex")
        
        if not all([job_id, project_id]):
            logger.error(f"Invalid job data in {job_file}")
            job_file.unlink(missing_ok=True)
            return

        project_dir = job_file.parent.parent.parent
        status_file = project_dir / "compile" / "status" / f"{job_id}.json"
        log_file = project_dir / "compile" / "logs" / f"{job_id}.txt"
        cancel_file = project_dir / "compile" / f"{job_id}.cancel"
        
        # Check for cancellation
        if cancel_file.exists():
            logger.info(f"Job {job_id} was cancelled")
            self._write_status(status_file, {
                "jobId": job_id,
                "projectId": project_id,
                "state": "canceled",
                "finishedAt": self._now_iso(),
            })
            job_file.unlink(missing_ok=True)
            cancel_file.unlink(missing_ok=True)
            return

        logger.info(f"Processing job {job_id} for project {project_id}")
        
        # Move job from queue to working
        working_dir = project_dir / "compile" / "working"
        working_dir.mkdir(exist_ok=True)
        working_job = working_dir / job_file.name
        shutil.move(str(job_file), str(working_job))
        
        started_at = self._now_iso()
        
        # Write initial status
        self._write_status(status_file, {
            "jobId": job_id,
            "projectId": project_id,
            "state": "running",
            "revision": job_data.get("revision", ""),
            "startedAt": started_at,
        })
        
        # Initialize log file
        log_file.parent.mkdir(exist_ok=True)
        with open(log_file, 'w') as f:
            f.write(f"LaTeX compilation started at {started_at}\n")
            f.write(f"Job ID: {job_id}\n")
            f.write(f"Project: {project_id}\n")
            f.write(f"Engine: {engine}\n")
            f.write(f"Entry file: {entry_file}\n\n")
        
        # Perform compilation
        success = self._compile_latex(project_dir, entry_file, engine, log_file, cancel_file)
        
        finished_at = self._now_iso()
        
        if success:
            self._write_status(status_file, {
                "jobId": job_id,
                "projectId": project_id,
                "state": "success",
                "revision": job_data.get("revision", ""),
                "startedAt": started_at,
                "finishedAt": finished_at,
            })
            logger.info(f"Job {job_id} completed successfully")
        else:
            self._write_status(status_file, {
                "jobId": job_id,
                "projectId": project_id,
                "state": "failed",
                "revision": job_data.get("revision", ""),
                "startedAt": started_at,
                "finishedAt": finished_at,
            })
            logger.info(f"Job {job_id} failed")
        
        # Clean up working file
        working_job.unlink(missing_ok=True)

    def _compile_latex(self, project_dir: Path, entry_file: str, engine: str, log_file: Path, cancel_file: Path) -> bool:
        """Compile LaTeX document"""
        entry_path = project_dir / entry_file
        if not entry_path.exists():
            with open(log_file, 'a') as f:
                f.write(f"ERROR: Entry file {entry_file} not found\n")
            return False
        
        # Change to project directory for compilation
        original_cwd = os.getcwd()
        os.chdir(str(project_dir))
        
        try:
            # Build command
            cmd = [
                engine,
                "-interaction=nonstopmode",
                "-halt-on-error",
                "-output-directory=.",
                entry_file
            ]
            
            with open(log_file, 'a') as f:
                f.write(f"Running: {' '.join(cmd)}\n\n")
            
            # Run LaTeX compilation
            process = subprocess.Popen(
                cmd,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
                bufsize=1,
                universal_newlines=True
            )
            
            # Stream output to log file and check for cancellation
            with open(log_file, 'a') as f:
                while True:
                    # Check for cancellation
                    if cancel_file.exists():
                        process.terminate()
                        f.write("\n--- COMPILATION CANCELLED ---\n")
                        return False
                    
                    output = process.stdout.readline()
                    if output == '' and process.poll() is not None:
                        break
                    if output:
                        f.write(output)
                        f.flush()
            
            return_code = process.poll()
            
            with open(log_file, 'a') as f:
                f.write(f"\nCompilation finished with return code: {return_code}\n")
            
            # Check if PDF was generated
            pdf_path = project_dir / "output.pdf"
            aux_files = list(project_dir.glob(f"{Path(entry_file).stem}.*"))
            
            # Find the generated PDF (might have different name)
            generated_pdf = None
            for aux_file in aux_files:
                if aux_file.suffix == '.pdf':
                    generated_pdf = aux_file
                    break
            
            if generated_pdf and generated_pdf.exists():
                # Move/copy to standard output.pdf location
                if generated_pdf != pdf_path:
                    shutil.copy2(str(generated_pdf), str(pdf_path))
                
                with open(log_file, 'a') as f:
                    f.write(f"PDF generated: output.pdf ({pdf_path.stat().st_size} bytes)\n")
                return True
            else:
                with open(log_file, 'a') as f:
                    f.write("ERROR: No PDF output generated\n")
                return False
                
        except subprocess.TimeoutExpired:
            with open(log_file, 'a') as f:
                f.write("ERROR: Compilation timed out\n")
            return False
        except Exception as e:
            with open(log_file, 'a') as f:
                f.write(f"ERROR: Compilation failed: {e}\n")
            return False
        finally:
            os.chdir(original_cwd)

    def _write_status(self, status_file: Path, status_data: Dict[str, Any]):
        """Write status JSON file"""
        status_file.parent.mkdir(exist_ok=True)
        with open(status_file, 'w') as f:
            json.dump(status_data, f, indent=2)

    def _now_iso(self) -> str:
        """Get current time in ISO format"""
        return datetime.now(timezone.utc).isoformat()


def main():
    work_dir = os.environ.get("LATEX_WORK_DIR", "/work/latex_files")
    worker = LatexCompilerWorker(work_dir)
    
    try:
        worker.run()
    except KeyboardInterrupt:
        logger.info("Received keyboard interrupt")
    except Exception as e:
        logger.error(f"Worker crashed: {e}")
        sys.exit(1)


if __name__ == "__main__":
    main()
