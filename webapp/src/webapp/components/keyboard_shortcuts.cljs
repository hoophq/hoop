(ns webapp.components.keyboard-shortcuts
  (:require
   ["lucide-react" :refer [Keyboard]]
   [reagent.core :as r]
   [re-frame.core :as rf]
   [webapp.config :as config]))

(defn- shortcut-item [{:keys [shortcut description]}]
  [:div {:class "flex justify-between items-center mb-1 text-xs"}
   [:span {:class "font-medium text-gray-900"}
    description]
   [:code {:class "bg-gray-100 px-2 py-1 rounded font-mono text-gray-700"}
    shortcut]])

(defn- shortcuts-content []
  [:div {:class "px-6 py-4 max-w-md"}
   [:div {:class "flex justify-between items-center mb-3"}
    [:h3 {:class "text-lg font-medium"} "Keyboard Shortcuts"]
    [:button {:class "text-gray-400 hover:text-gray-500"
              :on-click #(rf/dispatch [:modal->close])}
     "×"]]
    
   [:div {:class "mb-3"}
    [:h4 {:class "font-medium text-gray-900 mb-1"} "Execution"]
    [shortcut-item {:shortcut "⌘ + Enter" :description "Execute entire script"}]
    [shortcut-item {:shortcut "⌘ + Shift + Enter" :description "Execute selected text"}]]
   
   [:div {:class "mb-3"}
    [:h4 {:class "font-medium text-gray-900 mb-1"} "Navigation"]
    [shortcut-item {:shortcut "Alt + ←/→" :description "Move cursor to previous/next syntax boundary"}]
    [shortcut-item {:shortcut "⌘ + Shift + \\" :description "Jump to matching bracket"}]]
   
   [:div {:class "mb-3"} 
    [:h4 {:class "font-medium text-gray-900 mb-1"} "Editing"]
    [shortcut-item {:shortcut "Alt + ↑/↓" :description "Move line up/down"}]
    [shortcut-item {:shortcut "Shift + Alt + ↑/↓" :description "Copy line up/down"}]
    [shortcut-item {:shortcut "Alt + L" :description "Select current line"}]
    [shortcut-item {:shortcut "⌘ + I" :description "Select parent syntax"}]
    [shortcut-item {:shortcut "⌘ + [/]" :description "Decrease/increase indentation"}]
    [shortcut-item {:shortcut "⌘ + Alt + \\" :description "Indent selection"}]
    [shortcut-item {:shortcut "⌘ + Shift + K" :description "Delete line"}]
    [shortcut-item {:shortcut "⌘ + /" :description "Toggle comment"}]
    [shortcut-item {:shortcut "Alt + A" :description "Toggle block comment"}]]])

(defn keyboard-shortcuts-button []
  [:div
   [:div {:class "flex items-center gap-1 text-gray-600 cursor-pointer" 
          :on-click #(rf/dispatch [:modal->open {:content [shortcuts-content]
                                                :maxWidth "450px"}])}
    [:> Keyboard {:size 16}]
    [:span {:class "text-xs"} "Shortcuts"]]])