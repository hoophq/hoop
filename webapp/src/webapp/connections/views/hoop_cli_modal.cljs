(ns webapp.connections.views.hoop-cli-modal
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Tabs Text]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.config :as config]))

(defn cli-commands [api-url]
  {:macos {:install ["brew tap hoophq/hoopcli https://github.com/hoophq/hoopcli"
                     "brew install hoop"]
           :configure (str "hoop config create --api-url " api-url)
           :login "hoop login"
           :connect "hoop connect <connection-name>"
           :docs-link (get-in config/docs-url
                              [:clients :command-line :macos])}
   :linux {:install "curl -s -L https://releases.hoop.dev/release/install-cli.sh | sh"
           :configure (str "hoop config create --api-url " api-url)
           :login "hoop login"
           :connect "hoop connect <connection-name>"
           :docs-link (get-in config/docs-url
                              [:clients :command-line :linux])}
   :windows {:docs-link (get-in config/docs-url
                                [:clients :command-line :windows])}})

(defn highlight-command [cmd connection-name api-url]
  (let [processed-cmd (cs/replace cmd "<connection-name>" connection-name)]
    [:div
     (cond
       ;; Brew commands
       (cs/includes? cmd "brew tap brew")
       [:> Text {:size "2"}
        "brew tap brew "
        [:> Text {:size "2" :class "text-[--orange-7]"} "https://github.com/hoophq/brew"]]

       (cs/includes? cmd "brew install")
       [:> Text {:size "2"}
        "brew install hoop"]

       ;; Config command
       (cs/includes? cmd "hoop config create")
       [:> Text {:size "2"}
        "hoop config create --api-url "
        [:> Text {:size "2" :class "text-[--orange-7]"} api-url]]

       ;; Login command
       (cs/includes? cmd "hoop login")
       [:> Text {:size "2"} "hoop login"]

       ;; Connect command
       (cs/includes? cmd "hoop connect")
       [:> Text {:size "2"}
        "hoop connect "
        [:> Text {:size "2" :class "text-[--orange-7]"} connection-name]]

       ;; Curl command
       (cs/includes? cmd "curl")
       [:> Text {:size "2"}
        "curl -s -L "
        [:> Text {:size "2" :class "text-[--orange-7]"} "https://releases.hoop.dev/release/install-cli.sh"]
        " | sh"]

       ;; Default
       :else processed-cmd)]))

(defn code-block [commands connection-name api-url]
  [:div {:class "bg-gray-900 text-white p-4 rounded-lg font-mono text-sm space-y-2"}
   (if (vector? commands)
     (for [cmd commands]
       ^{:key cmd}
       [highlight-command cmd connection-name api-url])
     [highlight-command commands connection-name api-url])])

(defn cli-section [title commands connection-name api-url]
  [:div {:class "space-y-3"}
   [:> Text {:as "h3" :size "4" :weight "bold"}
    title]
   [code-block commands connection-name api-url]])

(defn macos-content [connection-name api-url]
  [:div {:class "space-y-radix-6"}
   [cli-section "Install Hoop CLI" (get-in (cli-commands api-url) [:macos :install]) connection-name api-url]
   [cli-section "Configure authentication" (get-in (cli-commands api-url) [:macos :configure]) connection-name api-url]
   [cli-section "Get an access token" (get-in (cli-commands api-url) [:macos :login]) connection-name api-url]
   [cli-section "Connect to resource" (get-in (cli-commands api-url) [:macos :connect]) connection-name api-url]])

(defn linux-content [connection-name api-url]
  [:div {:class "space-y-radix-6"}
   [cli-section "Install Hoop CLI" (get-in (cli-commands api-url) [:linux :install]) connection-name api-url]
   [cli-section "Configure authentication" (get-in (cli-commands api-url) [:linux :configure]) connection-name api-url]
   [cli-section "Get an access token" (get-in (cli-commands api-url) [:linux :login]) connection-name api-url]
   [cli-section "Connect to resource" (get-in (cli-commands api-url) [:linux :connect]) connection-name api-url]])

(defn windows-content [api-url]
  [:div {:class "space-y-6"}
   [:> Text {:as "h3" :size "4" :weight "medium"}
    "Install Hoop CLI"]
   [:> Text {:as "p" :size "2" :class "text-gray-11"}
    "For details about Windows installation check "
    [:a {:href (get-in (cli-commands api-url) [:windows :docs-link])
         :target "_blank"
         :class "text-blue-600 hover:text-blue-800 underline"}
     "Windows CLI documentation"]
    "."]])

(defn main [connection-name]
  (let [selected-tab (r/atom "macos")
        gateway-info (rf/subscribe [:gateway->info])]
    (fn [connection-name]
      [:div {:class "flex max-h-[696px] overflow-hidden -m-8"}
       ;; Left side - Content
       [:div {:class "w-[55%] flex-1 space-y-radix-8 my-10 px-10 overflow-y-auto"}
        [:div {:class "space-y-2"}
         [:> Text {:as "h2" :size "6" :weight "bold"}
          "Get more with Hoop CLI"]
         [:> Text {:as "p" :size "2" :class "text-gray-11"}
          "Follow the steps below to connect to your resource via native access."]]

        [:> Tabs.Root {:value @selected-tab
                       :onValueChange #(reset! selected-tab %)}
         [:> Tabs.List {:class "grid grid-cols-3 gap-1 mb-6"}
          [:> Tabs.Trigger {:value "macos" :class "px-4 py-2 text-sm"}
           "MacOS"]
          [:> Tabs.Trigger {:value "linux" :class "px-4 py-2 text-sm"}
           "Linux"]
          [:> Tabs.Trigger {:value "windows" :class "px-4 py-2 text-sm"}
           "Windows"]]

         [:> Tabs.Content {:value "macos"}
          [macos-content connection-name (:api_url (:data @gateway-info))]]

         [:> Tabs.Content {:value "linux"}
          [linux-content connection-name (:api_url (:data @gateway-info))]]

         [:> Tabs.Content {:value "windows"}
          [windows-content (:api_url (:data @gateway-info))]]]

        [:> Flex {:justify "between"}
         [:> Button {:variant "ghost"
                     :color "gray"
                     :on-click #(rf/dispatch [:modal->close])}
          "Close"]
         (when-not (= @selected-tab "windows")
           [:> Button {:variant "ghost"
                       :color "gray"
                       :on-click #(js/window.open
                                   (get-in (cli-commands (:api_url (:data @gateway-info)))
                                           [(keyword @selected-tab) :docs-link])
                                   "_blank")}
            "Hoop CLI Docs"])]]

       ;; Right side - Decorative image
       [:> Box {:class "w-[45%] bg-blue-50"}
        [:img {:src "/images/illustrations/cli-promotion.png"
               :alt "Hoop CLI Modal"
               :class "w-full h-full object-cover"}]]])))
