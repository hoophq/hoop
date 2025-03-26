(ns webapp.components.keyboard-shortcuts
  (:require
   ["lucide-react" :refer [Keyboard X]]
   [reagent.core :as r]
   [re-frame.core :as rf]
   [webapp.config :as config]
   ["@radix-ui/themes" :refer [Box Button Flex Text Heading Dialog Badge Code]]))

(defn- detect-os []
  (let [user-agent (.. js/window -navigator -userAgent)]
    (cond
      (re-find #"Mac" user-agent) :mac
      (re-find #"Windows" user-agent) :windows
      (re-find #"Linux" user-agent) :linux
      :else :unknown)))

(defn- get-key-symbol [key-combo]
  (let [os (detect-os)]
    (case os
      :mac key-combo
      :windows (-> key-combo
                  (clojure.string/replace "⌘" "Ctrl")
                  (clojure.string/replace "⌥" "Alt")
                  (clojure.string/replace "⇧" "Shift"))
      :linux (-> key-combo
                (clojure.string/replace "⌘" "Ctrl")
                (clojure.string/replace "⌥" "Alt")
                (clojure.string/replace "⇧" "Shift"))
      key-combo)))

(defn- shortcut-item [{:keys [shortcut description]}]
  [:> Flex {:justify "between" :align "center" :class "mb-1"}
   [:> Text {:size "2" :weight "medium"}
    description]
   [:> Code {:size "2" :variant "ghost" :color "gray"}
    (get-key-symbol shortcut)]])

(defn- shortcut-section [title & items]
  [:> Box {:class "mb-4"}
   [:> Text {:as "h4" :size "2" :weight "medium" :class "mb-2"}
    title]
   (for [item items]
     ^{:key (:description item)}
     [shortcut-item item])])

(defn- shortcuts-content []
  [:> Box {:class "p-5"}
   [:> Flex {:justify "between" :align "center" :class "mb-4"}
    [:> Heading {:size "4" :weight "medium"} "Keyboard Shortcuts"]
    [:> Button {:size "1" 
                :variant "ghost" 
                :color "gray" 
                :on-click #(rf/dispatch [:modal->close])}
     [:> X {:size 16}]]]
    
   [shortcut-section "Execution"
    {:shortcut "⌘ + Enter" :description "Execute entire script"}
    {:shortcut "⌘ + Shift + Enter" :description "Execute selected text"}]
   
   [shortcut-section "Navigation"
    {:shortcut "Alt + ←/→" :description "Move cursor to previous/next syntax boundary"}
    {:shortcut "⌘ + Shift + \\" :description "Jump to matching bracket"}]
   
   [shortcut-section "Editing"
    {:shortcut "Alt + ↑/↓" :description "Move line up/down"}
    {:shortcut "Shift + Alt + ↑/↓" :description "Copy line up/down"}
    {:shortcut "Alt + L" :description "Select current line"}
    {:shortcut "⌘ + I" :description "Select parent syntax"}
    {:shortcut "⌘ + [/]" :description "Decrease/increase indentation"}
    {:shortcut "⌘ + Alt + \\" :description "Indent selection"}
    {:shortcut "⌘ + Shift + K" :description "Delete line"}
    {:shortcut "⌘ + /" :description "Toggle comment"}
    {:shortcut "Alt + A" :description "Toggle block comment"}]])

(defn keyboard-shortcuts-button []
  [:> Button {:size "1" 
              :variant "ghost" 
              :color "gray"
              :class "flex items-center gap-1"
              :on-click #(rf/dispatch [:modal->open {:content [shortcuts-content]
                                                    :maxWidth "450px"}])}
   [:> Keyboard {:size 16}]
   [:> Text {:size "1"} "Shortcuts"]])