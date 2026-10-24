import pathlib, shutil, sys
shutil.copyfile(pathlib.Path(__file__).with_name('service_bad.txt'),pathlib.Path(sys.argv[1])/'service.py')
