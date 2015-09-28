import pymongo
import sys
import datetime
from optparse import OptionParser

# Inserts 3 global logs:
# Log 1: one line with 3MB of "1"
# Log 2: one line with 3MB of "2", one line with 3MB of "3"
# Log 3: one line with 3MB of "4"
# Checks that these become:
# Log 1: one line with 3MB of "1"
# Log 2: one line with 3MB of "2"
# Log 3: one line with 3MB of "3"
# Log 4: one line with 3MB of "4"
# Checks that all other log fields and the build seq are correct
# Repeats for non-global logs
def test():
	connection.drop_database("logkeeper_test")
	db = connection.logkeeper_test
	builds = db.builds
	tests = db.tests
	logs = db.logs

	line_size = 3 * 1024 * 1024 # 3MB
	dates = [datetime.datetime(2015, 1, 1, 0, 0, 0, 0), 
		datetime.datetime(2015, 2, 1, 0, 0, 0, 0), 
		datetime.datetime(2015, 2, 1, 0, 0, 0, 0), 
		datetime.datetime(2015, 3, 1, 0, 0, 0, 0)]

	builds.insert_one({"name": "global_build", "seq": 3})
	global_build = builds.find_one({"name": "global_build"})
	logs.insert_one({
		"build_id": global_build["_id"], 
		"seq": 1, 
		"started": dates[0], 
		"lines": [[datetime.datetime.utcnow(), "1" * line_size]]})
	logs.insert_one({
		"build_id": global_build["_id"], 
		"seq": 2, 
		"started": dates[1], 
		"lines": [
			[datetime.datetime.utcnow(), "2" * line_size], 
			[datetime.datetime.utcnow(), "3" * line_size]]})
	logs.insert_one({
		"build_id": global_build["_id"], 
		"seq": 3, 
		"started": dates[3], 
		"lines": [[datetime.datetime.utcnow(), "4" * line_size]]})

	builds.insert_one({"name": "build", "seq": 0})
	build = builds.find_one({"name": "build"})
	tests.insert_one({"name": "test", "build_id": build["_id"], "seq": 3})
	test = tests.find_one({"name": "test"})
	logs.insert_one({
		"build_id": build["_id"], 
		"test_id": test["_id"], 
		"seq": 1, 
		"started": dates[0], 
		"lines": [[datetime.datetime.utcnow(), "1" * line_size]]})
	logs.insert_one({
		"build_id": build["_id"], 
		"test_id": test["_id"], 
		"seq": 2, 
		"started": dates[1], 
		"lines": [
			[datetime.datetime.utcnow(), "2" * line_size], 
			[datetime.datetime.utcnow(), "3" * line_size]]})
	logs.insert_one({
		"build_id": build["_id"], 
		"test_id": test["_id"], 
		"seq": 3, 
		"started": dates[3], 
		"lines": [[datetime.datetime.utcnow(), "4" * line_size]]})

	break_up_large_documents(builds, tests, logs)

	if builds.find_one({"_id": global_build["_id"]})["seq"] != 4:
		print "Global build has wrong seq"
		return
	seq = 1
	for log in logs.find({"build_id": global_build["_id"], "test_id": None}).sort("seq", 
	pymongo.ASCENDING):
		if log["seq"] != seq:
			print "Global log ", seq, " has wrong seq: ", log["seq"]
			return
		if log["started"] != dates[seq - 1]:
			print "Global log ", seq, " has wrong started: ", log["started"]
			return
		if len(log["lines"]) != 1:
			print "Global log ", seq, " has wrong number of lines: ", len(log["lines"])
			return
		if log["lines"][0][1][0] != str(seq):
			print "Global log ", seq, " line has wrong first char: ", log["lines"][0][1][0]
			return
		seq = seq + 1

	if builds.find_one({"_id": build["_id"]})["seq"] != 0:
		print "Build has wrong seq"
		return
	if tests.find_one({"_id": test["_id"]})["seq"] != 4:
		print "Test has wrong seq"
		return
	seq = 1
	for log in logs.find({"build_id": build["_id"], "test_id": test["_id"]}).sort("seq", 
		pymongo.ASCENDING):
		if log["seq"] != seq:
			print "Log ", seq, " has wrong seq: ", log["seq"]
			return
		if log["started"] != dates[seq - 1]:
			print "Log ", seq, " has wrong started: ", log["started"]
			return
		if len(log["lines"]) != 1:
			print "Log ", seq, " has wrong number of lines: ", len(log["lines"])
			return
		if log["lines"][0][1][0] != str(seq):
			print "Log ", seq, " line has wrong first char: ", log["lines"][0][1][0]
			return
		seq = seq + 1

	print "All tests passed"
	connection.drop_database("logkeeper_test")

# Breaks up logs over 4MB
def break_up_large_documents(builds, tests, logs):

	max_size = 4 * 1024 * 1024 # 4MB
	i = 0

	for log in logs.find().sort("_id", pymongo.ASCENDING):
		if i % 10000 == 0 and i > 0:
			print "Checked ", i, " logs, now checking log with _id:", log["_id"]
		i = i + 1

		# Test if the log is too large
		size = 0
		for line in log["lines"]:
			size += len(line[1])
		if size <= max_size:
			continue

		print "Breaking up log"
		print "\t_id:", log["_id"]
		print "\tbuild_id:", log["build_id"]
		if "test_id" in log.keys():
			print "\ttest_id", log["test_id"]

		# Initialize a list of new logs
		# New log seq values will begin at log["seq"]
		seq = log["seq"] 
		new_log = {"build_id": log["build_id"], 
			"seq": seq, 
			"started": log["started"], 
			"lines": []}
		if "test_id" in log.keys():
			new_log["test_id"] = log["test_id"]
		new_log_size = 0
		new_logs = [new_log]

		# Break up lines of log into 4MB chunks
		for line in log["lines"]:
			# Check if new_log is full, and if so, create a new log
			if new_log_size + len(line[1]) > max_size:
				seq += 1
				new_log = {"build_id": log["build_id"], 
					"seq": seq, 
					"started": log["started"], 
					"lines": []}
				if "test_id" in log.keys():
					new_log["test_id"] = log["test_id"]
				new_log_size = 0
				new_logs.append(new_log)

			# Add the line to the current new_log and update its size	
			new_log["lines"].append(line)
			new_log_size = new_log_size + len(line[1])

		# Number of logs we have added
		inc = seq - log["seq"]
		# Increment seq values of later logs
		logs.update_many({
			"build_id": log["build_id"], 
			"test_id": log.get("test_id"), 
			"seq": {"$gt": log["seq"]}}, 
			{"$inc": {"seq": inc}})
		# Increment seq value of test
		if "test_id" in log.keys():
			tests.update_one({"_id": log["test_id"]}, {"$inc": {"seq": inc}})
		else:
			# Increment seq value of build
			builds.update_one({"_id": log["build_id"]}, {"$inc": {"seq": inc}})

		# Replace log with list of new logs	
		logs.insert_many(new_logs)
		logs.delete_one({"_id": log["_id"]})

parser = OptionParser()
parser.add_option("--test", dest="test", action="store_true", default=False)
parser.add_option("--host", dest="host", default="localhost")
(options, args) = parser.parse_args()
connection = pymongo.MongoClient("mongodb://" + options.host)
if options.test:
	test()
else:
	db = connection.buildlogs
	break_up_large_documents(db.builds, db.tests, db.logs)