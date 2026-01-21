// Base path for the REST WHEP API
const backend = window.location.origin;
const rest = '/whep';
let resource = null, token = null;
let cleanupDone = false;

// PeerConnection
let pc = null;
let iceUfrag = null, icePwd = null, candidates = [];

// Helper function to get query string arguments
function getQueryStringValue(name) {
	name = name.replace(/[\[]/, '\\[').replace(/[\]]/, '\\]');
	let regex = new RegExp('[\\?&]' + name + '=([^&#]*)'),
		results = regex.exec(location.search);
	return results === null ? '' : decodeURIComponent(results[1].replace(/\+/g, ' '));
}
// Get the endpoint ID to subscribe to
const id = getQueryStringValue('id');
// Check if we should let the endpoint send the offer
const expectOffer = (getQueryStringValue('offer') === 'false')

$(document).ready(function () {
	// Make sure WebRTC is supported by the browser
	if (!window.RTCPeerConnection) {
		bootbox.alert('WebRTC unsupported...');
		return;
	}
	if (!id) {
		bootbox.alert('Invalid endpoint ID...');
		return;
	}

	token = 'verysecret';
	subscribeToEndpoint();
	setupCleanupHandlers();

});

// Function to subscribe to the WHEP endpoint
async function subscribeToEndpoint() {
	let headers = null, offer = null;
	if (token)
		headers = { Authorization: 'Bearer ' + token };
	if (!expectOffer) {
		// We need to prepare an offer ourselves, do it now
		let iceServers = [{ urls: "stun:stun.l.google.com:19302" }];
		createPeerConnectionIfNeeded(iceServers);
		let transceiver = await pc.addTransceiver('audio');
		if (transceiver.setDirection)
			transceiver.setDirection('recvonly');
		else
			transceiver.direction = 'recvonly';
		transceiver = await pc.addTransceiver('video');
		if (transceiver.setDirection)
			transceiver.setDirection('recvonly');
		else
			transceiver.direction = 'recvonly';
		offer = await pc.createOffer({});
		await pc.setLocalDescription(offer);
		// Extract ICE ufrag and pwd (for trickle)
		iceUfrag = offer.sdp.match(/a=ice-ufrag:(.*)\r\n/)[1];
		icePwd = offer.sdp.match(/a=ice-pwd:(.*)\r\n/)[1];
	}
	// Contact the WHEP endpoint
	$.ajax({
		url: backend + rest + '/endpoint/' + id,
		type: 'POST',
		headers: headers,
		contentType: offer ? 'application/sdp' : null,
		data: offer ? offer.sdp : {}
	}).error(function (xhr, textStatus, errorThrown) {
		bootbox.alert(xhr.status + ": " + xhr.responseText);
	}).success(function (sdp, textStatus, request) {
		console.log('Got SDP:', sdp);
		resource = request.getResponseHeader('Location');
		console.log('WHEP resource:', resource);
		// TODO Parse ICE servers
		// let ice = request.getResponseHeader('Link');
		let iceServers = [{ urls: "stun:stun.l.google.com:19302" }];
		// Create PeerConnection, if needed
		createPeerConnectionIfNeeded(iceServers);
		// Pass the SDP to the PeerConnection
		let jsep = {
			type: expectOffer ? 'offer' : 'answer',
			sdp: sdp
		};
		pc.setRemoteDescription(jsep)
			.then(function () {
				console.log('Remote description accepted');
				if (!expectOffer) {
					// We're done: just check if we have candidates to send
					if (candidates.length > 0) {
						// FIXME Trickle candidate
						let headers = null;
						if (token)
							headers = { Authorization: 'Bearer ' + token };
						let candidate =
							'a=ice-ufrag:' + iceUfrag + '\r\n' +
							'a=ice-pwd:' + icePwd + '\r\n' +
							'm=audio 9 RTP/AVP 0\r\n';
						for (let c of candidates)
							candidate += 'a=' + c + '\r\n';
						candidates = [];
						$.ajax({
							url: backend + resource,
							type: 'PATCH',
							headers: headers,
							contentType: 'application/trickle-ice-sdpfrag',
							data: candidate
						}).error(function (xhr, textStatus, errorThrown) {
							bootbox.alert(xhr.status + ": " + xhr.responseText);
						}).done(function (response) {
							console.log('Candidate sent');
						});
					}
					return;
				}
				// If we got here, we're in the "WHEP server sends offer" mode,
				// so we have to prepare an answer to send back via a PATCH
				pc.createAnswer({})
					.then(function (answer) {
						console.log('Prepared answer:', answer.sdp);
						// Extract ICE ufrag and pwd (for trickle)
						iceUfrag = answer.sdp.match(/a=ice-ufrag:(.*)\r\n/)[1];
						icePwd = answer.sdp.match(/a=ice-pwd:(.*)\r\n/)[1];
						pc.setLocalDescription(answer)
							.then(function () {
								console.log('Sending answer to WHEP server');
								// Send the answer to the resource address
								$.ajax({
									url: backend + resource,
									type: 'PATCH',
									headers: headers,
									contentType: 'application/sdp',
									data: answer.sdp
								}).error(function (xhr, textStatus, errorThrown) {
									bootbox.alert(xhr.status + ": " + xhr.responseText);
								}).done(function (response) {
									console.log('Negotiation completed');
								});
							}, function (err) {
								bootbox.alert(err.message);
							});
					}, function (err) {
						bootbox.alert(err.message);
					});
			}, function (err) {
				bootbox.alert(err.message);
			});
	});
}

// Helper function to attach a media stream to a video element
function attachMediaStream(element, stream) {
	try {
		element.srcObject = stream;
	} catch (e) {
		try {
			element.src = URL.createObjectURL(stream);
		} catch (e) {
			Janus.error("Error attaching stream to element", e);
		}
	}
};

// Helper function to create a PeerConnection, if needed, since we can either
// expect an offer from the WHEP server, or provide one ourselves
function createPeerConnectionIfNeeded(iceServers) {
	if (pc)
		return;
	let pc_config = {
		sdpSemantics: 'unified-plan',
		iceServers: iceServers
	};
	pc = new RTCPeerConnection(pc_config);
	pc.oniceconnectionstatechange = function () {
		console.log('[ICE] ', pc.iceConnectionState);
	};
	pc.onicecandidate = function (event) {
		let end = false;
		if (!event.candidate || (event.candidate.candidate && event.candidate.candidate.indexOf('endOfCandidates') > 0)) {
			console.log('End of candidates');
			end = true;
		} else {
			console.log('Got candidate:', event.candidate.candidate);
		}
		if (!resource) {
			console.log('No resource URL yet, queueing candidate');
			candidates.push(end ? 'end-of-candidates' : event.candidate.candidate);
			return;
		}
		if (!iceUfrag || !icePwd) {
			console.log('No ICE credentials yet, queueing candidate');
			candidates.push(end ? 'end-of-candidates' : event.candidate.candidate);
			return;
		}
		// FIXME Trickle candidate
		let headers = null;
		if (token)
			headers = { Authorization: 'Bearer ' + token };
		let candidate =
			'a=ice-ufrag:' + iceUfrag + '\r\n' +
			'a=ice-pwd:' + icePwd + '\r\n' +
			'm=audio 9 RTP/AVP 0\r\n' +
			'a=' + (end ? 'end-of-candidates' : event.candidate.candidate) + '\r\n';
		$.ajax({
			url: backend + resource,
			type: 'PATCH',
			headers: headers,
			contentType: 'application/trickle-ice-sdpfrag',
			data: candidate
		}).error(function (xhr, textStatus, errorThrown) {
			bootbox.alert(xhr.status + ": " + xhr.responseText);
		}).done(function (response) {
			console.log('Candidate sent');
		});
	};
	pc.ontrack = function (event) {
		console.log('Handling Remote Track', event);
		if (!event.streams)
			return;
		console.warn(event.streams[0].getTracks());
		if ($('#whepvideo').length === 0) {
			$('#video').removeClass('hide').show();
			// Video muted per autoplay
			$('#videoremote').append('<video class="rounded centered" id="whepvideo" width="100%" height="100%" autoplay muted playsinline/>');
			// Bottone unmute
			$('#videoremote').append('<button id="unmute-btn" style="position: absolute; top: 10px; left: 10px; z-index: 1000; padding: 10px 20px; background: #318aeaff; color: white; border: none; border-radius: 5px; cursor: pointer; font-size: 14px;">Click to Unmute</button>');

			// Handler unmute
			$('#unmute-btn').on('click', function () {
				const video = $('#whepvideo').get(0);
				video.muted = false;
				video.volume = 1;
				$(this).text('Audio On');
				$(this).css('background', '#48de6bff');
				setTimeout(() => $(this).fadeOut(), 2000);
			});
		}
		attachMediaStream($('#whepvideo').get(0), event.streams[0]);
		$('#whepvideo').get(0).play();
		$('#whepvideo').get(0).volume = 1;
	};
}

// Cleanup
function cleanupConnection() {
	if (cleanupDone) return;
	cleanupDone = true;

	console.log('Cleaning up WHEP connection...');

	// Close PeerConnection
	if (pc) {
		try {
			pc.close();
			pc = null;
		} catch (e) {
			console.error('Exception closing PC:', e);
		}
	}

	// DELETE
	if (resource) {
		try {
			fetch(backend + resource, {
				method: 'DELETE',
				headers: token ? { 'Authorization': 'Bearer ' + token } : {},
				keepalive: true  // Funziona anche con pagina chiusa
			}).catch(err => console.error('Cleanup error:', err));
			console.log('Cleanup DELETE sent');
		} catch (e) {
			console.error('Exception during cleanup:', e);
		}
	}

	resource = null;
	candidates = [];
	iceUfrag = null;
	icePwd = null;
}

function setupCleanupHandlers() {
	// Trigger cleanup when page closes
	window.addEventListener('beforeunload', function (e) {
		console.log('Page unloading - triggering cleanup');
		cleanupConnection();
	});

	// Cleanup on page error
	window.addEventListener('error', function (e) {
		console.error('Page error - triggering cleanup:', e);
		cleanupConnection();
	});
}