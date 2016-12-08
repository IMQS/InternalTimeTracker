

function show_bar_chart(data) {
	var options = {
	  width: 500,
	  height: 300
	};

	//var data = {
	//	labels: [1, 2, 3, 4],
	//	series: [[5, 2, 8, 3], [1, 3, 7, 2]]
	//};

	new Chartist.Bar('#user_chart', data, options);
}

function show_user_report(userid) {
	var good = function(resp) {
		resp = JSON.parse(resp.response);
		var data = {
			labels: [],
			series: [[],[]],
		}
		for (var i = 0; i < resp.Months.length; i++) {
			var m = resp.Months[i];
			data.labels.push(m.Month);
			data.series[0].push(m.FeatureSeconds / 3600);
			data.series[1].push(m.BugSeconds / 3600);
		}
		show_bar_chart(data);
	};
	$http({method: "GET", url: "/user?userid=" + userid, good: good});
}

$id('select_user').onchange = function(t) {
	show_user_report(t.target.value);
};

